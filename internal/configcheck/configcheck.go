// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Package configcheck implements --validate-config: it checks the whole
// configuration, offline first and then against the database where a
// connection can be made, and reports every problem rather than the first.
package configcheck

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/neo4j/neo4j-go-driver/v6/neo4j"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/database"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/readiness"
	"github.com/neo4j-labs/neo4j-mcp-canary/internal/server"
)

type Severity string

const (
	SeverityError Severity = "ERROR"
	SeverityWarn  Severity = "WARN"
	SeverityInfo  Severity = "INFO"
)

// Finding is one issue (or notice) found in the configuration.
type Finding struct {
	Severity Severity
	Message  string
}

// Connector opens a database connection for the online checks and returns
// a cleanup func. It is only called for single-instance STDIO
// configurations, the one case where the server holds Neo4j credentials.
type Connector func(ctx context.Context, cfg *config.Config) (database.Service, func(), error)

// UnknownKeyWarnings returns one warning per config file key that matches no
// parameter. The server also logs these at normal startup.
func UnknownKeyWarnings(path string) []string {
	if path == "" {
		return nil
	}
	keys, err := config.UnknownFileKeys(path)
	if err != nil {
		return nil // a file that can't be read is reported by loading it
	}
	msgs := make([]string, len(keys))
	for i, k := range keys {
		msgs[i] = fmt.Sprintf("config file %q: unknown key %q is ignored, so its setting keeps the default. Keys are the lower-cased environment variable names, e.g. neo4j_uri, neo4j_read_only.", path, k)
	}
	return msgs
}

// Run validates the configuration selected by overrides, writes the report
// to w, and returns the process exit code: 1 if any error was found, else 0.
func Run(ctx context.Context, w io.Writer, overrides config.CLIOverrides, connect Connector) int {
	var findings []Finding
	add := func(sev Severity, format string, args ...any) {
		findings = append(findings, Finding{sev, fmt.Sprintf(format, args...)})
	}

	for _, m := range UnknownKeyWarnings(config.ConfigFilePath(overrides)) {
		add(SeverityWarn, "%s", m)
	}

	cfg, errs := config.LoadConfigAll(overrides)
	for _, err := range errs {
		add(SeverityError, "%v", err)
	}

	if cfg != nil {
		for _, issue := range server.CheckToolConfig(cfg, nil) {
			sev := SeverityWarn
			if issue.Error {
				sev = SeverityError
			}
			add(sev, "%s", issue.Message)
		}
		if !hasError(findings) {
			seen := map[string]bool{}
			for _, f := range findings {
				seen[f.Message] = true
			}
			// The online pass repeats the offline tool checks to add the
			// capability ones; drop what was already reported.
			for _, f := range onlineChecks(ctx, cfg, connect) {
				if !seen[f.Message] {
					findings = append(findings, f)
				}
			}
		} else {
			add(SeverityInfo, "database checks skipped: fix the errors above first.")
		}
	}

	return write(w, findings)
}

func hasError(fs []Finding) bool {
	for _, f := range fs {
		if f.Severity == SeverityError {
			return true
		}
	}
	return false
}

func write(w io.Writer, findings []Finding) int {
	var errors, warnings int
	for _, f := range findings {
		fmt.Fprintf(w, "%-5s %s\n", f.Severity, f.Message)
		switch f.Severity {
		case SeverityError:
			errors++
		case SeverityWarn:
			warnings++
		}
	}
	if errors > 0 {
		fmt.Fprintf(w, "\nConfiguration is invalid: %d error(s), %d warning(s).\n", errors, warnings)
		return 1
	}
	if warnings > 0 {
		fmt.Fprintf(w, "\nConfiguration is valid with %d warning(s).\n", warnings)
		return 0
	}
	fmt.Fprintln(w, "Configuration is valid.")
	return 0
}

func onlineChecks(ctx context.Context, cfg *config.Config, connect Connector) []Finding {
	switch {
	case len(cfg.Instances) > 0:
		return []Finding{{SeverityInfo, "database checks skipped: multi-instance mode connects per request, so custom tool statements and server capabilities were not checked against Neo4j."}}
	case cfg.TransportMode == config.TransportModeHTTP:
		return []Finding{{SeverityInfo, "database checks skipped: in HTTP mode Neo4j credentials are supplied per request, so custom tool statements and server capabilities were not checked against Neo4j."}}
	}

	db, cleanup, err := connect(ctx, cfg)
	if err != nil {
		return []Finding{{SeverityError, fmt.Sprintf("cannot connect to Neo4j at %s: %v. Check NEO4J_URI and the credentials, and that the server is running.", cfg.URI, err)}}
	}
	defer cleanup()

	caps, err := readiness.NewChecker(db, cfg.URI).Verify(ctx)
	if err != nil {
		return []Finding{{SeverityError, fmt.Sprintf("Neo4j at %s is not ready for use: %v", cfg.URI, err)}}
	}

	var findings []Finding
	for _, issue := range server.CheckToolConfig(cfg, &caps) {
		sev := SeverityWarn
		if issue.Error {
			sev = SeverityError
		}
		findings = append(findings, Finding{sev, issue.Message})
	}
	for _, t := range cfg.CustomTools {
		findings = append(findings, checkCustomTool(ctx, db, cfg, t)...)
	}
	return findings
}

// checkCustomTool EXPLAINs the tool's statement with sample values of each
// parameter's declared type. EXPLAIN plans without executing, so nothing is
// read or written.
func checkCustomTool(ctx context.Context, db database.Service, cfg *config.Config, t config.CustomTool) []Finding {
	if cfg.CypherTimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(cfg.CypherTimeoutSeconds)*time.Second)
		defer cancel()
	}
	queryType, err := db.GetQueryType(ctx, t.Statement, sampleParams(t))
	if err != nil {
		return []Finding{{SeverityError, fmt.Sprintf("custom tool %q: Neo4j rejected the statement: %v", t.Name, err)}}
	}
	switch {
	case t.Access == config.CustomToolAccessRead && queryType != neo4j.QueryTypeReadOnly:
		return []Finding{{SeverityError, fmt.Sprintf("custom tool %q is declared access: read but its statement is not read-only (query type %q), so every call would be refused. Set access: readwrite, or change the statement.", t.Name, queryType)}}
	case t.Access == config.CustomToolAccessReadWrite && queryType == neo4j.QueryTypeReadOnly:
		return []Finding{{SeverityWarn, fmt.Sprintf("custom tool %q is declared access: readwrite but its statement only reads. Use access: read so it stays available when NEO4J_READ_ONLY is true.", t.Name)}}
	}
	return nil
}

// sampleParams returns a valid value for each parameter: its default, else
// its first enum value, else a placeholder of the declared type.
func sampleParams(t config.CustomTool) map[string]any {
	params := make(map[string]any, len(t.Params))
	for _, p := range t.Params {
		var raw any
		switch {
		case p.Default != nil:
			raw = p.Default
		case len(p.Enum) > 0:
			raw = p.Enum[0]
		default:
			switch p.Type {
			case config.CustomParamInteger, config.CustomParamNumber:
				raw = 1.0
			case config.CustomParamBoolean:
				raw = true
			default:
				raw = "sample"
			}
		}
		v, err := config.CoerceCustomParam(p.Type, raw)
		if err != nil {
			v = raw
		}
		params[p.Name] = v
	}
	return params
}
