// Command test-server runs the CLM API mock (pkg/client/clmtest) as a standalone,
// long-lived HTTP service. Use it to point a real baton-docusign binary at a working
// CLM backend via --clm-base-url when the connected DocuSign account has no CLM
// subscription of its own — eSignature calls still go to the real DocuSign API and
// authenticate normally; only CLM calls are redirected here.
//
// Usage:
//
//	go run ./cmd/test-server
//	baton-docusign --refresh-token=... \
//	    --clm-base-url=http://localhost:8765 -f sync.c1z
package main

import (
	"fmt"
	"os"

	"github.com/conductorone/baton-docusign/pkg/client/clmtest"
)

const listenAddr = "localhost:8765"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	return clmtest.RunStandalone(listenAddr)
}
