// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package queryapi

import (
	"fmt"
	"net/url"
	"strings"
)

// Mode identifies which wire protocol the configured Neo4j URI implies.
type Mode int

const (
	// ModeBolt means the URI uses a Bolt-family scheme (bolt, bolt+s,
	// bolt+ssc, neo4j, neo4j+s, neo4j+ssc) and the existing Bolt driver
	// should be used.
	ModeBolt Mode = iota
	// ModeQueryAPI means the URI uses an HTTP-family scheme (http, https)
	// and the Query API should be used instead of the Bolt driver.
	ModeQueryAPI
)

// DetectMode inspects the scheme of uri and reports which backend should be
// used to talk to Neo4j: the Query API for http/https, the Bolt driver for
// everything else (bolt, bolt+s, bolt+ssc, neo4j, neo4j+s, neo4j+ssc).
//
// This is a pure string check on the scheme — no network access — so it can
// run before any connection is attempted.
func DetectMode(uri string) (Mode, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return ModeBolt, fmt.Errorf("failed to parse Neo4j URI %q: %w", uri, err)
	}

	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
		return ModeQueryAPI, nil
	default:
		return ModeBolt, nil
	}
}
