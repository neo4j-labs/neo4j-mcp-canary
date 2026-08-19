// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cli

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/neo4j-labs/neo4j-mcp-canary/internal/config"
)

// osExit is a variable that can be mocked in tests
var osExit = os.Exit

// configFileFlagName is the one CLI flag that isn't part of config.Fields():
// it locates the config file itself, so it must exist before a file source
// can be built.
const configFileFlagName = "config-file"

// argsSlice lists every known configuration flag, derived from
// config.Fields() plus --config-file. HandleArgs pre-scans os.Args for these
// so it can skip past them (and their values) before the stdlib flag package
// parses them in ParseConfigFlags.
var argsSlice = buildArgsSlice()

func buildArgsSlice() []string {
	names := make([]string, 0, len(config.Fields())+1)
	for _, f := range config.Fields() {
		if f.FlagName != "" {
			names = append(names, "--"+f.FlagName)
		}
	}
	names = append(names, "--"+configFileFlagName)
	return names
}

// helpText is generated from config.Fields() so the CLI flags, required env
// vars, and optional env vars documented here can never drift from what
// ParseConfigFlags actually registers.
var helpText = buildHelpText()

func buildHelpText() string {
	var options, required, optional strings.Builder

	for _, f := range config.Fields() {
		if f.FlagName != "" {
			fmt.Fprintf(&options, "  --%s <%s>\n      %s (overrides environment variable %s)\n", f.FlagName, f.Placeholder, f.Description, f.EnvVar)
		}
		if f.Required {
			fmt.Fprintf(&required, "  %-12s %s\n", f.EnvVar, f.Description)
			continue
		}
		defaultSuffix := ""
		if f.DefaultDisplay != "" {
			defaultSuffix = fmt.Sprintf(" (default: %s)", f.DefaultDisplay)
		}
		fmt.Fprintf(&optional, "  %s %s%s\n", f.EnvVar, f.Description, defaultSuffix)
	}
	fmt.Fprintf(&options, "  --%s <PATH>\n      Path to an optional JSON or YAML config file, used as a lowest-priority configuration source (overrides environment variable NEO4J_CONFIG_FILE)\n", configFileFlagName)
	fmt.Fprintf(&optional, "  NEO4J_CONFIG_FILE Path to an optional JSON or YAML config file, used as a lowest-priority configuration source\n")

	return fmt.Sprintf(`neo4j-mcp-canary - Neo4j Model Context Protocol Canary Server

Usage:
  neo4j-mcp-canary [OPTIONS]

Options:
  -h, --help                          Show this help message
  -v, --version                       Show version information
%s
Required Environment Variables:
%s
Optional Environment Variables:
%s
  Examples:
  # Using environment variables
  NEO4J_URI=bolt://localhost:7687 NEO4J_USERNAME=neo4j NEO4J_PASSWORD=password neo4j-mcp-canary

  # Using CLI flags (takes precedence over environment variables)
  neo4j-mcp-canary --neo4j-uri bolt://localhost:7687 --neo4j-username neo4j --neo4j-password password

  # Using a config file (lowest-priority source; CLI flags and env vars still override it)
  neo4j-mcp-canary --config-file /etc/neo4j-mcp/config.yaml

For more information, visit: https://github.com/neo4j-labs/neo4j-mcp-canary
`, options.String(), required.String(), optional.String())
}

// ParseConfigFlags parses CLI flags and returns configuration overrides
// ready to pass to config.LoadConfig. It should be called after HandleArgs
// to ensure help/version flags are processed first.
func ParseConfigFlags() config.CLIOverrides {
	flagValues := make(map[string]*string, len(config.Fields()))
	for _, f := range config.Fields() {
		if f.FlagName == "" {
			continue
		}
		flagValues[f.Name] = flag.String(f.FlagName, "", fmt.Sprintf("%s (overrides %s env var)", f.Description, f.EnvVar))
	}
	configFile := flag.String(configFileFlagName, "", "Path to an optional JSON or YAML config file (overrides NEO4J_CONFIG_FILE env var)")

	flag.Parse()

	overrides := config.CLIOverrides{}
	for name, value := range flagValues {
		if *value != "" {
			overrides[name] = *value
		}
	}
	if *configFile != "" {
		overrides["ConfigFile"] = *configFile
	}
	return overrides
}

// HandleArgs processes command-line arguments for version and help flags.
// It exits the program after displaying the requested information.
// If unknown flags are encountered, it prints an error message and exits.
// Known configuration flags are skipped here so that the flag package in main.go can handle them properly.
func HandleArgs(version string) {
	if len(os.Args) <= 1 {
		return
	}

	flags := make(map[string]bool)
	var err error
	i := 1 // we start from 1 because os.Args[0] is the program name ("neo4j-mcp") - not a flag

	for i < len(os.Args) {
		arg := os.Args[i]

		// Allow configuration flags to be parsed by the flag package
		if slices.Contains(argsSlice, arg) {
			// Check if there's a value following the flag
			if i+1 >= len(os.Args) {
				err = fmt.Errorf("%s requires a value", arg)
				break
			}
			// Check if next argument is another flag (starts with -)
			nextArg := os.Args[i+1]
			if strings.HasPrefix(nextArg, "-") {
				err = fmt.Errorf("%s requires a value (got flag %s instead)", arg, nextArg)
				break
			}
			// Safe to skip flag and value - let flag package handle them
			i += 2
			continue
		}

		switch arg {
		case "-h", "--help":
			flags["help"] = true
			i++
		case "-v", "--version":
			flags["version"] = true
			i++
		default:
			if arg == "--" {
				// Stop processing our flags, let flag package handle the rest
				i = len(os.Args)
			} else {
				err = fmt.Errorf("unknown flag or argument: %s", arg)
				i++
			}
		}
		// Exit loop if an error occurred
		if err != nil {
			break
		}
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		osExit(1)
	}

	if flags["help"] {
		fmt.Print(helpText)
		osExit(0)
	}

	if flags["version"] {
		fmt.Printf("neo4j-mcp-canary version: %s\n", version)
		osExit(0)
	}
}
