// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Detects the calling prcoess

package parentprocess

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/tklauser/ps"
)

// Gets the pid of the parent process
// for use by public functions in this package
func ppid() int {
	// Get current Process ID and Parent Process ID
	return os.Getppid()
}

// Returns full path of the process that
// called this Go application.  The path includes the binary
func Fullpath() (*string, error) {
	var ppidname string

	// Get parent process details
	p, err := ps.FindProcess(ppid())
	if err != nil {
		slog.Error("Failed to obtain the parent process full path", "error", err)
		return nil, err
	}

	ppidname = p.ExecutablePath()

	slog.Debug("Parent process full path", "name", ppidname)

	return &ppidname, nil

}

// Filename returns the file name of the binary of the process that
// called this application (e.g. "zsh", "Code Helper"), without its directory,
// so no home directory or username reaches analytics. It returns "" when the
// parent cannot be determined.
func Filename() (*string, error) {

	var name string

	p, err := ps.FindProcess(ppid())
	if err != nil {
		slog.Error("Failed to obtain the parent process", "error", err)
		return nil, err
	}

	path := p.ExecutablePath()
	if path == "" {
		return nil, nil
	}
	name = filepath.Base(path)

	slog.Debug("Parent process binary", "name", name)

	return &name, nil
}
