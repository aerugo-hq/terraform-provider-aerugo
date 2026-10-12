package provider

import (
	"context"
	"net/http"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/client"
)

var (
	_ datasource.DataSource              = &teamDataSource{}
	_ datasource.DataSourceWithConfigure = &teamDataSource{}
	_ datasource.DataSource              = &statusDataSource{}
	_ datasource.DataSourceWithConfigure = &statusDataSource{}
)

// ------------------------------------------------------------------ data "aerugo_team"

type teamDataSource struct {
	client *client.Client
}

type teamDataModel struct {
	Slug            types.String `tfsdk:"slug"`
	Name            types.String `tfsdk:"name"`
	Discoverability types.String `tfsdk:"discoverability"`
	Roles           types.List   `tfsdk:"roles"`
	ManagedBy       types.String `tfsdk:"managed_by"`
}

// NewTeamDataSource is data "aerugo_team".
func NewTeamDataSource() datasource.DataSource { return &teamDataSource{} }

func (d *teamDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team"
}

func (d *teamDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A team that exists already, managed elsewhere or by hand: to refer to it without managing it.",
		Attributes: map[string]schema.Attribute{
			"slug": schema.StringAttribute{
				Required:   true,
				Validators: []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)},
			},
			"name":            schema.StringAttribute{Computed: true},
			"discoverability": schema.StringAttribute{Computed: true, Description: "org or private."},
			"roles": schema.ListAttribute{
				Computed:    true,
				ElementType: types.StringType,
				Description: "The service account's own roles on the team.",
			},
			"managed_by": schema.StringAttribute{
				Computed:    true,
				Description: "The tool that manages the team, such as terraform, or empty if it's managed by hand.",
			},
		},
	}
}

func (d *teamDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (d *teamDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var cfg teamDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var got struct {
		Slug            string   `json:"slug"`
		Name            string   `json:"name"`
		Discoverability string   `json:"discoverability"`
		Roles           []string `json:"roles"`
		ManagedBy       *struct {
			By string `json:"by"`
		} `json:"managed_by"`
	}
	if err := d.client.Do(ctx, http.MethodGet, "/v1/workspaces/"+url.PathEscape(cfg.Slug.ValueString()), nil, nil, &got); err != nil {
		resp.Diagnostics.AddError("Couldn't read the team", err.Error())
		return
	}
	roles, diags := types.ListValueFrom(ctx, types.StringType, got.Roles)
	resp.Diagnostics.Append(diags...)
	manager := ""
	if got.ManagedBy != nil {
		manager = got.ManagedBy.By
	}
	state := teamDataModel{
		Slug:            types.StringValue(got.Slug),
		Name:            types.StringValue(got.Name),
		Discoverability: types.StringValue(got.Discoverability),
		Roles:           roles,
		ManagedBy:       types.StringValue(manager),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// ------------------------------------------------------------------ data "aerugo_status"

type statusDataSource struct {
	client *client.Client
}

type statusDataModel struct {
	Version types.String `tfsdk:"version"`
	OK      types.Bool   `tfsdk:"ok"`
	Checks  types.Map    `tfsdk:"checks"`
}

// NewStatusDataSource is data "aerugo_status".
func NewStatusDataSource() datasource.DataSource { return &statusDataSource{} }

func (d *statusDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_status"
}

func (d *statusDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "The API's version and whether each part it depends on answers: a check for a pipeline to run first. " +
			"Up or down only, never an address or an error.",
		Attributes: map[string]schema.Attribute{
			"version": schema.StringAttribute{Computed: true},
			"ok":      schema.BoolAttribute{Computed: true, Description: "Whether every part answers."},
			"checks": schema.MapAttribute{
				Computed:    true,
				ElementType: types.BoolType,
				Description: "Each part (database, authorization, secrets, sign_in) and whether it answers.",
			},
		},
	}
}

func (d *statusDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (d *statusDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	var got struct {
		Version string          `json:"version"`
		OK      bool            `json:"ok"`
		Checks  map[string]bool `json:"checks"`
	}
	if err := d.client.Do(ctx, http.MethodGet, "/v1/status", nil, nil, &got); err != nil {
		resp.Diagnostics.AddError("Couldn't read Aerugo's status", err.Error())
		return
	}
	checks, diags := types.MapValueFrom(ctx, types.BoolType, got.Checks)
	resp.Diagnostics.Append(diags...)
	state := statusDataModel{Version: types.StringValue(got.Version), OK: types.BoolValue(got.OK), Checks: checks}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
