// Command oidcmock runs the test OIDC issuer (internal/testkit/oidctest) as a
// standalone server, standing in for Keycloak in dashboard end-to-end tests.
//
// Playwright selects the next user with:
//
//	POST /test/login-as {"subject":"u1","email":"dev@example.com","groups":["qanat-users"]}
//
// TEST ONLY. It signs tokens for anyone who asks and must never be deployed.
package main

import (
	"flag"
	"log"

	"qanat/internal/testkit/oidctest"
)

func main() {
	addr := flag.String("addr", ":9180", "listen address")
	public := flag.String("public-url", "http://localhost:9180", "URL browsers and the control plane use to reach this issuer")
	flag.Parse()
	log.Printf("oidcmock (TEST ONLY) on %s, issuer %s/realms/qanat", *addr, *public)
	log.Fatal(oidctest.Serve(*addr, *public))
}
