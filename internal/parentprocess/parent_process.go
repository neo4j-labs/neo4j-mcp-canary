// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

// Detects the calling prcoess

package parentprocess

import (
	"os"

	"github.com/tklauser/ps"
)

// Returns full path of the process that
// called this Go application.  The path includes the binary
func Fullpath() (*string, error) {
	var ppidname string

	// Get current Process ID and Parent Process ID
	parentPID := os.Getppid()

	// Get parent process details
	p, err := ps.FindProcess(parentPID)
	if err != nil {
		return nil, err
	}

	ppidname = p.ExecutablePath()

	return &ppidname, nil

}
