//go:build qanat

// Driver for the real Qanat stack (deploy/test/compose.test.yml, profile
// "stack"). It talks to the gateway through Nginx and seeds state through the
// control plane's Admin API plus its test-only endpoints, which exist only
// when QANAT_TEST_MODE=1.
//
// P0 deliverable: implement the methods below against the Admin API. Until
// then this driver fails every test on purpose, so the acceptance CI job can
// never go green by silently skipping.
package acceptance

import (
	"context"
	"errors"
	"os"
	"testing"
)

func init() {
	RegisterDriver(func(t testing.TB) Driver {
		gw, ctl := os.Getenv("QANAT_ACCEPTANCE_GATEWAY"), os.Getenv("QANAT_ACCEPTANCE_CONTROL")
		if gw == "" || ctl == "" {
			t.Fatal("QANAT_ACCEPTANCE_GATEWAY and QANAT_ACCEPTANCE_CONTROL must be set (use `make test-acceptance`)")
		}
		return &qanatDriver{gateway: gw, control: ctl}
	})
}

var errNotImplemented = errors.New("qanat acceptance driver: not implemented yet (see docs/testing.md#the-qanat-driver)")

type qanatDriver struct{ gateway, control string }

func (d *qanatDriver) GatewayURL() string          { return d.gateway }
func (d *qanatDriver) Reset(context.Context) error { return errNotImplemented }
func (d *qanatDriver) CreateUser(context.Context, string) (string, error) {
	return "", errNotImplemented
}
func (d *qanatDriver) SetPlan(context.Context, string, Plan) error { return errNotImplemented }
func (d *qanatDriver) CreateKey(context.Context, string) (string, error) {
	return "", errNotImplemented
}
func (d *qanatDriver) SetMapping(context.Context, []MappingRule) error { return errNotImplemented }
func (d *qanatDriver) SetPrice(context.Context, string, Price) error   { return errNotImplemented }
func (d *qanatDriver) Usage(context.Context, string) (Usage, error) {
	return Usage{}, errNotImplemented
}
func (d *qanatDriver) AddUpstreamAccount(context.Context, string, string) (string, error) {
	return "", errNotImplemented
}
