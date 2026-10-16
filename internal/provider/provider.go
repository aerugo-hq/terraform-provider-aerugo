// Package provider is Aerugo's Terraform provider: one provider for every deployment, self-hosted or SaaS, where
// only the endpoint and the service account change.
package provider

import (
	"context"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/client"
)

var _ provider.Provider = &aerugoProvider{}

type aerugoProvider struct {
	version string
}

type providerModel struct {
	Endpoint         types.String `tfsdk:"endpoint"`
	TokenURL         types.String `tfsdk:"token_url"`
	ClientID         types.String `tfsdk:"client_id"`
	ClientSecret     types.String `tfsdk:"client_secret"`
	ManagedWorkspace types.String `tfsdk:"managed_workspace"`
}

// New returns the provider, at the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider { return &aerugoProvider{version: version} }
}

func (p *aerugoProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "aerugo"
	resp.Version = p.version
}

func (p *aerugoProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Manage an Aerugo org as code: its teams and members, the models each team asks with, the sources " +
			"that bring knowledge in, and the rules that keep it. The same provider works against a self-hosted Aerugo " +
			"and the SaaS; only the endpoint and the service account change.",
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				Optional:    true,
				Description: "The Aerugo API's address, such as https://acme.aerugo.cloud. Or set AERUGO_ENDPOINT.",
			},
			"token_url": schema.StringAttribute{
				Optional:    true,
				Description: "Your identity provider's token endpoint, where the service account signs in. Or set AERUGO_TOKEN_URL.",
			},
			"client_id": schema.StringAttribute{
				Optional:    true,
				Description: "The service account's OAuth client, registered by an org admin under Developer. Or set AERUGO_CLIENT_ID.",
			},
			"client_secret": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				Description: "The service account's client secret. Prefer AERUGO_CLIENT_SECRET, so it's never written in a configuration.",
			},
			"managed_workspace": schema.StringAttribute{
				Optional: true,
				Description: "A name for this Terraform setup, such as prod. The app shows it beside what Terraform manages. " +
					"Or set AERUGO_MANAGED_WORKSPACE.",
			},
		},
	}
}

func (p *aerugoProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	settings := []struct {
		name  string
		value types.String
		env   string
	}{
		{"endpoint", cfg.Endpoint, "AERUGO_ENDPOINT"},
		{"token_url", cfg.TokenURL, "AERUGO_TOKEN_URL"},
		{"client_id", cfg.ClientID, "AERUGO_CLIENT_ID"},
		{"client_secret", cfg.ClientSecret, "AERUGO_CLIENT_SECRET"},
		{"managed_workspace", cfg.ManagedWorkspace, "AERUGO_MANAGED_WORKSPACE"},
	}
	values := map[string]string{}
	for _, s := range settings {
		if s.value.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(s.name), "Unknown Aerugo setting",
				"This isn't known until apply. Set it directly, or with "+s.env+".")
			continue
		}
		if !s.value.IsNull() {
			values[s.name] = s.value.ValueString()
		} else {
			values[s.name] = os.Getenv(s.env)
		}
	}
	for _, s := range settings[:4] { // all but managed_workspace are needed to sign in
		if values[s.name] == "" {
			resp.Diagnostics.AddAttributeError(path.Root(s.name), "Missing Aerugo setting",
				"Set "+s.name+" in the provider block, or "+s.env+" in the environment.")
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}
	c, err := client.New(client.Config{
		Endpoint:         values["endpoint"],
		TokenURL:         values["token_url"],
		ClientID:         values["client_id"],
		ClientSecret:     values["client_secret"],
		ManagedWorkspace: values["managed_workspace"],
		UserAgent:        "terraform-provider-aerugo/" + p.version,
	})
	if err != nil {
		resp.Diagnostics.AddError("Couldn't set up the Aerugo client", err.Error())
		return
	}
	resp.ResourceData = c
	resp.DataSourceData = c
}

func (p *aerugoProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewTeamResource,
		NewTeamMemberResource,
		NewModelResource,
		NewModelBudgetResource,
		NewServiceAccountResource,
		NewConnectionResource,
		NewPipelineResource,
		NewRouteResource,
		NewRetentionPolicyResource,
		NewLegalHoldResource,
	}
}

func (p *aerugoProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewTeamDataSource,
		NewStatusDataSource,
	}
}
