package provider_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/winebarrel/terraform-provider-tfcode/internal/provider"
)

var factories = map[string]func() (tfprotov6.ProviderServer, error){
	"tfcode": providerserver.NewProtocol6WithError(provider.New("test")()),
}

// The harness serves the provider from the test process, so a relative source
// is resolved against this package's directory.
const dataSource = `
data "tfcode" "t" {
  source = "testdata/target"
}
`

func TestReadsValues(t *testing.T) {
	tests := map[string]string{
		`data.tfcode.t.outputs.env`:                                          "prod",
		`data.tfcode.t.outputs.secret`:                                       "shown",
		`data.tfcode.t.locals.tags.a`:                                        "x",
		`data.tfcode.t.locals.ports[1]`:                                      "443",
		`tostring(data.tfcode.t.locals.nothing == null)`:                     "true",
		`join(",", data.tfcode.t.locals.names)`:                              "a,b",
		`data.tfcode.t.resource.aws_route53_record.myhost.zone_id`:           "Z123",
		`data.tfcode.t.resource.aws_route53_record.myhost.name`:              "myhost.example.com",
		`data.tfcode.t.resource.aws_route53_record.myhost.ttl`:               "300",
		`data.tfcode.t.resource.aws_route53_record.myhost.proxied`:           "false",
		`data.tfcode.t.resource.aws_route53_record.many[1].name`:             "host-1",
		`data.tfcode.t.resource.aws_security_group.web.ingress[1].from_port`: "443",
	}
	for expr, want := range tests {
		t.Run(expr, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps: []resource.TestStep{{
					Config: dataSource + `output "test" { value = ` + expr + ` }`,
					Check:  resource.TestCheckOutput("test", want),
				}},
			})
		})
	}
}

func TestUndeterminedReferenceFails(t *testing.T) {
	for _, expr := range []string{
		`data.tfcode.t.outputs.undetermined`,
		`data.tfcode.t.locals.tags.b`,
		`data.tfcode.t.resource.aws_route53_record.myhost.fqdn`,
		`data.tfcode.t.resource.aws_route53_zone.main.zone_id`,
	} {
		t.Run(expr, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps: []resource.TestStep{{
					Config:      dataSource + `output "test" { value = ` + expr + ` }`,
					ExpectError: regexp.MustCompile(`Unsupported attribute`),
				}},
			})
		})
	}
}

func TestInvalidSource(t *testing.T) {
	tests := map[string]string{
		"/etc":             `must be a relative path`,
		"testdata/nothing": `no \.tf files`,
	}
	for source, want := range tests {
		t.Run(source, func(t *testing.T) {
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories,
				Steps: []resource.TestStep{{
					Config:      `data "tfcode" "t" { source = "` + source + `" }`,
					ExpectError: regexp.MustCompile(want),
				}},
			})
		})
	}
}
