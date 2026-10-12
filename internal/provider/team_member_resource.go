package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
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
	_ resource.Resource                = &teamMemberResource{}
	_ resource.ResourceWithConfigure   = &teamMemberResource{}
	_ resource.ResourceWithImportState = &teamMemberResource{}
)

var teamRoles = []string{"member", "curator", "admin", "auditor"}

type teamMemberResource struct {
	client *client.Client
}

type teamMemberModel struct {
	ID        types.String `tfsdk:"id"`
	Team      types.String `tfsdk:"team"`
	Principal types.String `tfsdk:"principal"`
	Role      types.String `tfsdk:"role"`
}

// NewTeamMemberResource is aerugo_team_member.
func NewTeamMemberResource() resource.Resource { return &teamMemberResource{} }

func (r *teamMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_team_member"
}

func (r *teamMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A role on a team for a person, a group, or a service account. It takes effect on their next request. " +
			"A grant is its team, principal, and role together, so changing any of them replaces it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "team/principal/role, the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"team": schema.StringAttribute{
				Required:      true,
				Description:   "The team's slug.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)},
			},
			"principal": schema.StringAttribute{
				Required:      true,
				Description:   "Who gets the role: user:<username>, group:<group>, or user:service-account-<client> for a service account.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.RegexMatches(principalPattern, "must look like user:alice or group:payments")},
			},
			"role": schema.StringAttribute{
				Required:      true,
				Description:   "member, curator, admin, or auditor.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.OneOf(teamRoles...)},
			},
		},
	}
}

func (r *teamMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func memberID(team, principal, role string) string { return team + "/" + principal + "/" + role }

func membersPath(team string) string { return "/v1/workspaces/" + url.PathEscape(team) + "/members" }

func (r *teamMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan teamMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := map[string]string{"principal": plan.Principal.ValueString(), "role": plan.Role.ValueString()}
	if err := r.client.Do(ctx, http.MethodPut, membersPath(plan.Team.ValueString()), nil, in, nil); err != nil {
		resp.Diagnostics.AddError("Couldn't give the role", err.Error())
		return
	}
	plan.ID = types.StringValue(memberID(plan.Team.ValueString(), plan.Principal.ValueString(), plan.Role.ValueString()))
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *teamMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state teamMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var got struct {
		Members []struct {
			Principal string `json:"principal"`
			Role      string `json:"role"`
		} `json:"members"`
	}
	err := r.client.Do(ctx, http.MethodGet, membersPath(state.Team.ValueString()), nil, nil, &got)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx) // the team is gone, or out of reach
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the team's members", err.Error())
		return
	}
	for _, m := range got.Members {
		if m.Principal == state.Principal.ValueString() && m.Role == state.Role.ValueString() {
			state.ID = types.StringValue(memberID(state.Team.ValueString(), m.Principal, m.Role))
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx) // the role was taken away in the app: the next plan offers to give it again
}

// Update is never asked to change anything, since every attribute replaces the grant.
func (r *teamMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan teamMemberModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *teamMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state teamMemberModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	query := url.Values{"principal": {state.Principal.ValueString()}, "role": {state.Role.ValueString()}}
	err := r.client.Do(ctx, http.MethodDelete, membersPath(state.Team.ValueString()), query, nil, nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Couldn't take the role away", err.Error())
	}
}

func (r *teamMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		resp.Diagnostics.AddError("Unexpected import ID",
			fmt.Sprintf("Expected team/principal/role, such as payments/user:alice/admin; got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("principal"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("role"), parts[2])...)
}
