// Package provider implements the tfcode provider, whose one data source reads
// static values out of another Terraform configuration's source files.
package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

var _ provider.Provider = &TfcodeProvider{}

type TfcodeProvider struct {
	version string
}

func (p *TfcodeProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "tfcode"
	resp.Version = p.version
}

func (p *TfcodeProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{}
}

func (p *TfcodeProvider) Configure(_ context.Context, _ provider.ConfigureRequest, _ *provider.ConfigureResponse) {
}

func (p *TfcodeProvider) Resources(_ context.Context) []func() resource.Resource {
	return nil
}

func (p *TfcodeProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{NewTfcodeDataSource}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &TfcodeProvider{version: version}
	}
}
