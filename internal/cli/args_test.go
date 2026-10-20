// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package cli

import (
	"flag"
	"io"
	"os"
	"strings"
	"testing"
)

const (
	testVersion     = "1.0.0"
	testProgramName = "neo4j-mcp-canary"
	testHelpText    = "neo4j-mcp-canary - Neo4j Model Context Protocol Canary Server"
	testVersionText = "neo4j-mcp-canary version: 1.0.0"
)

// captureOutput temporarily redirects stdout and stderr to capture output.
func captureOutput(fn func()) (stdout, stderr string) {
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()

	oldStdout := os.Stdout
	oldStderr := os.Stderr
	os.Stdout = wOut
	os.Stderr = wErr

	fn()

	wOut.Close()
	wErr.Close()
	os.Stdout = oldStdout
	os.Stderr = oldStderr

	outBytes, _ := io.ReadAll(rOut)
	errBytes, _ := io.ReadAll(rErr)

	return string(outBytes), string(errBytes)
}

// exitMock captures os.Exit calls for testing.
type exitMock struct {
	called bool
	code   int
}

// mockExit records the exit call and panics to stop execution.
func (m *exitMock) Exit(code int) {
	m.called = true
	m.code = code
	panic(m)
}

func TestHandleArgs(t *testing.T) {
	tests := []struct {
		name             string
		args             []string
		version          string
		expectedExitCode int    // -1 means no exit, 0 or 1 for exit codes
		expectedOutput   string // substring to find in stdout or stderr
		expectedStderr   string // substring to find in stderr (if non-empty, output is checked in stderr instead of stdout)
	}{
		{
			name:             "no flags",
			args:             []string{testProgramName},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "version flag short form",
			args:             []string{testProgramName, "-v"},
			version:          testVersion,
			expectedExitCode: 0,
			expectedOutput:   testVersionText,
		},
		{
			name:             "version flag long form",
			args:             []string{testProgramName, "--version"},
			version:          testVersion,
			expectedExitCode: 0,
			expectedOutput:   testVersionText,
		},
		{
			name:             "help flag short form",
			args:             []string{testProgramName, "-h"},
			version:          testVersion,
			expectedExitCode: 0,
			expectedOutput:   testHelpText,
		},
		{
			name:             "help flag long form",
			args:             []string{testProgramName, "--help"},
			version:          testVersion,
			expectedExitCode: 0,
			expectedOutput:   testHelpText,
		},
		{
			name:             "unknown flag",
			args:             []string{testProgramName, "-x"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "unknown flag or argument: -x",
		},
		{
			name:             "version flag with extra arguments",
			args:             []string{testProgramName, "-v", "extra"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "unknown flag or argument: extra",
		},
		{
			name:             "version flag at end",
			args:             []string{testProgramName, "extra", "-v"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "unknown flag or argument: extra",
		},
		{
			name:             "help and version flags together - help takes precedence",
			args:             []string{testProgramName, "-v", "-h"},
			version:          testVersion,
			expectedExitCode: 0,
			expectedOutput:   testHelpText,
		},
		{
			name:             "help flag at end",
			args:             []string{testProgramName, "extra", "-h"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "unknown flag or argument: extra",
		},
		{
			name:             "neo4j-uri configuration flag",
			args:             []string{testProgramName, "--neo4j-uri", "bolt://localhost:7687"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flag is allowed
		},
		{
			name:             "configuration flag in --name=value form",
			args:             []string{testProgramName, "--neo4j-uri=bolt://localhost:7687"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "config-file flag in --name=value form",
			args:             []string{testProgramName, "--config-file=/etc/neo4j-mcp/config.yaml"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "configuration flag in --name=value form with an empty value",
			args:             []string{testProgramName, "--neo4j-database="},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "--name=value mixed with the separate-value form",
			args:             []string{testProgramName, "--neo4j-uri=bolt://localhost:7687", "--neo4j-username", "user"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "validate-config flag",
			args:             []string{testProgramName, "--validate-config"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "validate-config flag with =true",
			args:             []string{testProgramName, "--validate-config=true"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "validate-config flag with =false",
			args:             []string{testProgramName, "--validate-config=false"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "unknown flag in --name=value form is still rejected",
			args:             []string{testProgramName, "--neo4j-nope=1"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "unknown flag or argument: --neo4j-nope=1",
		},
		{
			name:             "--name=value does not hide a following unknown argument",
			args:             []string{testProgramName, "--neo4j-uri=bolt://localhost:7687", "extra"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "unknown flag or argument: extra",
		},
		{
			name:             "multiple configuration flags",
			args:             []string{testProgramName, "--neo4j-uri", "bolt://localhost:7687", "--neo4j-username", "user"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flags are allowed
		},
		{
			name:             "configuration flag missing value - at end",
			args:             []string{testProgramName, "--neo4j-uri"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-uri requires a value",
		},
		{
			name:             "configuration flag missing value - followed by another flag",
			args:             []string{testProgramName, "--neo4j-uri", "--neo4j-username", "user"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-uri requires a value (got flag --neo4j-username instead)",
		},
		{
			name:             "neo4j-password missing value",
			args:             []string{testProgramName, "--neo4j-password"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-password requires a value",
		},
		{
			name:             "neo4j-database missing value - followed by another flag",
			args:             []string{testProgramName, "--neo4j-database", "--neo4j-uri", "bolt://localhost"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-database requires a value (got flag --neo4j-uri instead)",
		},
		{
			name:             "configuration flags with valid values",
			args:             []string{testProgramName, "--neo4j-uri", "bolt://localhost:7687", "--neo4j-username", "neo4j", "--neo4j-password", "password", "--neo4j-database", "neo4j"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit
		},
		{
			name:             "schema sample size flag with valid value",
			args:             []string{testProgramName, "--neo4j-schema-sample-size", "500"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit
		},
		{
			name:             "schema sample size flag missing value",
			args:             []string{testProgramName, "--neo4j-schema-sample-size"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-schema-sample-size requires a value",
		},
		// Cypher operational controls — each flag gets a valid-value case (to
		// confirm HandleArgs accepts the flag without exiting) and a missing-value
		// case (to confirm the standard "requires a value" error fires).
		{
			name:             "cypher max rows flag with valid value",
			args:             []string{testProgramName, "--neo4j-cypher-max-rows", "500"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "cypher max rows flag missing value",
			args:             []string{testProgramName, "--neo4j-cypher-max-rows"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-cypher-max-rows requires a value",
		},
		{
			name:             "cypher max bytes flag with valid value",
			args:             []string{testProgramName, "--neo4j-cypher-max-bytes", "500000"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "cypher max bytes flag missing value",
			args:             []string{testProgramName, "--neo4j-cypher-max-bytes"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-cypher-max-bytes requires a value",
		},
		{
			name:             "cypher timeout flag with valid value",
			args:             []string{testProgramName, "--neo4j-cypher-timeout", "60"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "cypher timeout flag missing value",
			args:             []string{testProgramName, "--neo4j-cypher-timeout"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-cypher-timeout requires a value",
		},
		{
			name:             "cypher max estimated rows flag with valid value",
			args:             []string{testProgramName, "--neo4j-cypher-max-estimated-rows", "1000000"},
			version:          testVersion,
			expectedExitCode: -1,
		},
		{
			name:             "cypher max estimated rows flag missing value",
			args:             []string{testProgramName, "--neo4j-cypher-max-estimated-rows"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-cypher-max-estimated-rows requires a value",
		},
		// Adjacency check: a cypher flag followed by another flag (rather than a
		// value) must surface the more descriptive error form. Pinning one case
		// of this is sufficient — the underlying detection logic is shared across
		// all flags in argsSlice.
		{
			name:             "cypher max bytes missing value - followed by another flag",
			args:             []string{testProgramName, "--neo4j-cypher-max-bytes", "--neo4j-uri", "bolt://localhost:7687"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-cypher-max-bytes requires a value (got flag --neo4j-uri instead)",
		},
		{
			name:             "transport mode flag valid value",
			args:             []string{testProgramName, "--neo4j-transport-mode", "http"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flag is allowed
		},
		{
			name:             "transport mode flag missing value",
			args:             []string{testProgramName, "--neo4j-transport-mode"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-transport-mode requires a value",
		},
		{
			name:             "transport mode flag missing value followed by another flag",
			args:             []string{testProgramName, "--neo4j-transport-mode", "--neo4j-uri", "bolt://localhost:7687"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-transport-mode requires a value (got flag --neo4j-uri instead)",
		},
		{
			name:             "http tls enabled flag with valid value",
			args:             []string{testProgramName, "--neo4j-http-tls-enabled", "true"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flag is allowed
		},
		{
			name:             "http tls enabled flag missing value",
			args:             []string{testProgramName, "--neo4j-http-tls-enabled"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-http-tls-enabled requires a value",
		},
		{
			name:             "http tls cert file flag with valid value",
			args:             []string{testProgramName, "--neo4j-http-tls-cert-file", "/path/to/cert.pem"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flag is allowed
		},
		{
			name:             "http tls cert file flag missing value",
			args:             []string{testProgramName, "--neo4j-http-tls-cert-file"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-http-tls-cert-file requires a value",
		},
		{
			name:             "http tls key file flag with valid value",
			args:             []string{testProgramName, "--neo4j-http-tls-key-file", "/path/to/key.pem"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flag is allowed
		},
		{
			name:             "http tls key file flag missing value",
			args:             []string{testProgramName, "--neo4j-http-tls-key-file"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-http-tls-key-file requires a value",
		},
		{
			name:             "http allowed origins flag with valid value",
			args:             []string{testProgramName, "--neo4j-http-allowed-origins", "https://example.com"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flag is allowed
		},
		{
			name:             "http allowed origins flag with multiple origins",
			args:             []string{testProgramName, "--neo4j-http-allowed-origins", "https://example.com,https://example2.com"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flag is allowed
		},
		{
			name:             "http allowed origins flag missing value",
			args:             []string{testProgramName, "--neo4j-http-allowed-origins"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-http-allowed-origins requires a value",
		},
		{
			name:             "http auth header name flag with valid value",
			args:             []string{testProgramName, "--neo4j-http-auth-header-name", "X-Custom-Auth"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flag is allowed
		},
		{
			name:             "http auth header name flag missing value",
			args:             []string{testProgramName, "--neo4j-http-auth-header-name"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-http-auth-header-name requires a value",
		},
		{
			name:             "double dash separator stops flag processing",
			args:             []string{testProgramName, "--", "--unknown-flag"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, -- stops our flag processing
		},
		{
			name:             "double dash separator with config flags before it",
			args:             []string{testProgramName, "--neo4j-uri", "bolt://localhost:7687", "--", "--unknown-flag"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, config flag before -- is valid
		},
		{
			name:             "http allow unauthenticated ping with valid value",
			args:             []string{testProgramName, "--neo4j-http-allow-unauthenticated-ping", "true"},
			version:          testVersion,
			expectedExitCode: -1, // Should not exit, flag is allowed
		},
		{
			name:             "http allow unauthenticated ping with missing value",
			args:             []string{testProgramName, "--neo4j-http-allow-unauthenticated-ping"},
			version:          testVersion,
			expectedExitCode: 1,
			expectedStderr:   "--neo4j-http-allow-unauthenticated-ping requires a value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			originalArgs := os.Args
			originalOsExit := osExit
			t.Cleanup(func() {
				os.Args = originalArgs
				osExit = originalOsExit
			})

			os.Args = tt.args
			mock := &exitMock{}
			osExit = mock.Exit

			stdout, stderr := captureOutput(func() {
				defer func() {
					if r := recover(); r != mock {
						if r != nil {
							panic(r)
						}
					}
				}()
				HandleArgs(tt.version)
			})

			// Verify exit behaviour
			shouldExit := tt.expectedExitCode != -1
			if shouldExit != mock.called {
				t.Errorf("exit called: got %v, want %v", mock.called, shouldExit)
			}

			if mock.called && mock.code != tt.expectedExitCode {
				t.Errorf("exit code: got %d, want %d", mock.code, tt.expectedExitCode)
			}

			// Verify stderr output
			if tt.expectedStderr != "" {
				if !strings.Contains(stderr, tt.expectedStderr) {
					t.Errorf("stderr: got %q, want to contain %q", stderr, tt.expectedStderr)
				}
			}

			// Verify output
			if tt.expectedOutput != "" {
				if !strings.Contains(stdout, tt.expectedOutput) {
					t.Errorf("stdout: got %q, want to contain %q", stdout, tt.expectedOutput)
				}
			}
		})
	}
}

func TestParseConfigFlags_AcceptsBothValueForms(t *testing.T) {
	oldArgs, oldFlags := os.Args, flag.CommandLine
	defer func() { os.Args, flag.CommandLine = oldArgs, oldFlags }()

	for name, tc := range map[string]struct {
		args         []string
		wantURI      string
		wantFile     string
		wantValidate bool
	}{
		"separate values": {[]string{testProgramName, "--neo4j-uri", "bolt://a:7687", "--config-file", "c.yaml", "--validate-config"}, "bolt://a:7687", "c.yaml", true},
		"=value forms":    {[]string{testProgramName, "--neo4j-uri=bolt://a:7687", "--config-file=c.yaml", "--validate-config=true"}, "bolt://a:7687", "c.yaml", true},
		"=false":          {[]string{testProgramName, "--neo4j-uri=bolt://a:7687", "--validate-config=false"}, "bolt://a:7687", "", false},
		"none":            {[]string{testProgramName}, "", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			flag.CommandLine = flag.NewFlagSet(testProgramName, flag.ContinueOnError)
			os.Args = tc.args

			overrides := ParseConfigFlags()

			if got := overrides["URI"]; got != tc.wantURI {
				t.Errorf("URI = %q, want %q", got, tc.wantURI)
			}
			if got := overrides["ConfigFile"]; got != tc.wantFile {
				t.Errorf("ConfigFile = %q, want %q", got, tc.wantFile)
			}
			if got := overrides[ValidateConfigKey] == "true"; got != tc.wantValidate {
				t.Errorf("validate-config = %v, want %v", got, tc.wantValidate)
			}
		})
	}
}
