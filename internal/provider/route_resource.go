package provider

import (
	"context"
	"net/http"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/client"
)

var (
	_ resource.Resource                = &routeResource{}
	_ resource.ResourceWithConfigure   = &routeResource{}
	_ resource.ResourceWithImportState = &routeResource{}
)

// One GitHub repository's documents sent to another team instead of its connection's (slice F1). Who may read them
// still comes from GitHub; the route only chooses where they're kept. The caller administers both teams.
type routeResource struct{ client *client.Client }

type routeModel struct {
	ID         types.String `tfsdk:"id"`
	Team       types.String `tfsdk:"team"`
	Connection types.String `tfsdk:"connection_name"`
	Repository types.String `tfsdk:"repository"`
	ToTeam     types.String `tfsdk:"to_team"`
}

// NewRouteResource is aerugo_connection_route.
func NewRouteResource() resource.Resource { return &routeResource{} }

func (r *routeResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_connection_route"
}

func (r *routeResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	slug := []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)}
	resp.Schema = schema.Schema{
		Description: "Send one repository of a GitHub connection to another team (slice F1). Destroying it sends the " +
			"repository back to the connection's own team. Its documents move on the next sync.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "team/connection/owner/repository, the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"team":            schema.StringAttribute{Required: true, Description: "The connection's team.", PlanModifiers: replace, Validators: slug},
			"connection_name": schema.StringAttribute{Required: true, Description: "The GitHub connection's name (connection is a word Terraform keeps for itself).", PlanModifiers: replace, Validators: slug},
			"repository": schema.StringAttribute{Required: true, Description: "owner/name, one the connection brings in.", PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(repositoryPattern, "must be owner/name")}},
			"to_team": schema.StringAttribute{Required: true, Description: "The team its documents go to. Changes in place.", Validators: slug},
		},
	}
}

func (r *routeResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *routeResource) put(ctx context.Context, m *routeModel, to any) error {
	return r.client.Do(ctx, http.MethodPut, connectionPath(m.Team.ValueString(), m.Connection.ValueString())+"/routes", nil,
		map[string]any{"repository": m.Repository.ValueString(), "workspace": to}, nil)
}

func routeID(m *routeModel) types.String {
	return types.StringValue(strings.Join([]string{m.Team.ValueString(), m.Connection.ValueString(), m.Repository.ValueString()}, "/"))
}

func (r *routeResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan routeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &plan, plan.ToTeam.ValueString()); err != nil {
		resp.Diagnostics.AddError("Couldn't route the repository", err.Error())
		return
	}
	plan.ID = routeID(&plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *routeResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state routeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var got struct {
		Containers []struct {
			ExternalID string `json:"external_id"`
			Team       string `json:"team"`
		} `json:"containers"`
	}
	err := r.client.Do(ctx, http.MethodGet, connectionPath(state.Team.ValueString(), state.Connection.ValueString()), nil, nil, &got)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the route", err.Error())
		return
	}
	for _, c := range got.Containers {
		if c.ExternalID == state.Repository.ValueString() && c.Team != state.Team.ValueString() {
			state.ToTeam, state.ID = types.StringValue(c.Team), routeID(&state)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx) // back with its connection's own team, or no longer brought in
}

func (r *routeResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan routeModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &plan, plan.ToTeam.ValueString()); err != nil {
		resp.Diagnostics.AddError("Couldn't change the route", err.Error())
		return
	}
	plan.ID = routeID(&plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *routeResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state routeModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &state, nil); err != nil && !client.IsNotFound(err) { // no team: back to its connection's own
		resp.Diagnostics.AddError("Couldn't send the repository back", err.Error())
	}
}

func (r *routeResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := splitID(req.ID, 4, "team/connection/owner/repository, such as payments/github/acme/ledger", &resp.Diagnostics)
	if parts == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("connection_name"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("repository"), parts[2]+"/"+parts[3])...)
}
