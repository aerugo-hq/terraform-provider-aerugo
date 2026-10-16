package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"

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
	_ resource.Resource                = &pipelineResource{}
	_ resource.ResourceWithConfigure   = &pipelineResource{}
	_ resource.ResourceWithImportState = &pipelineResource{}
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,99}/[A-Za-z0-9._-]{1,100}$`)

// A CI pipeline registered to push into a team (slice J1): its repository's workload tokens, from a trusted issuer,
// push documents with no key at all. Every attribute names who may push, so changing one registers it again.
type pipelineResource struct {
	client *client.Client
}

type pipelineModel struct {
	ID         types.String `tfsdk:"id"`
	Team       types.String `tfsdk:"team"`
	Name       types.String `tfsdk:"name"`
	Issuer     types.String `tfsdk:"issuer"`
	Repository types.String `tfsdk:"repository"`
	Ref        types.String `tfsdk:"ref"`
}

type pipelineAPI struct {
	Name       string  `json:"name"`
	Issuer     string  `json:"issuer"`
	Repository string  `json:"repository"`
	Ref        *string `json:"ref"`
}

// NewPipelineResource is aerugo_pipeline.
func NewPipelineResource() resource.Resource { return &pipelineResource{} }

func (r *pipelineResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_pipeline"
}

func (r *pipelineResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A CI pipeline that may push documents into a team with its own workload tokens (no key anywhere): " +
			"one repository, on one ref if given, from an issuer the platform trusts. Its team's admins register it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "team/name, the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"team": schema.StringAttribute{Required: true, Description: "The team's slug.", PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)}},
			"name": schema.StringAttribute{Required: true, Description: "The pipeline's name in its team.", PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)}},
			"issuer": schema.StringAttribute{Required: true, PlanModifiers: replace,
				Description: "The CI's token issuer, one the platform trusts, like https://token.actions.githubusercontent.com.",
				Validators:  []validator.String{stringvalidator.LengthBetween(8, 300)}},
			"repository": schema.StringAttribute{Required: true, Description: "owner/name: the only repository whose tokens may push.",
				PlanModifiers: replace, Validators: []validator.String{stringvalidator.RegexMatches(repositoryPattern, "must be owner/name")}},
			"ref": schema.StringAttribute{Optional: true, PlanModifiers: replace,
				Description: "Only tokens for this ref, like refs/heads/main; unset, any ref of the repository.",
				Validators:  []validator.String{stringvalidator.LengthBetween(1, 300)}},
		},
	}
}

func (r *pipelineResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func pipelinesPath(team string) string {
	return "/v1/workspaces/" + url.PathEscape(team) + "/pipelines"
}

func (r *pipelineResource) fetch(ctx context.Context, team, name string) (*pipelineAPI, error) {
	var got struct {
		Pipelines []pipelineAPI `json:"pipelines"`
	}
	err := r.client.Do(ctx, http.MethodGet, pipelinesPath(team), nil, nil, &got)
	if client.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for _, p := range got.Pipelines {
		if p.Name == name {
			return &p, nil
		}
	}
	return nil, nil
}

func (r *pipelineResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan pipelineModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := map[string]any{"name": plan.Name.ValueString(), "issuer": plan.Issuer.ValueString(), "repository": plan.Repository.ValueString()}
	if connSet(plan.Ref) {
		in["ref"] = plan.Ref.ValueString()
	}
	if err := r.client.Do(ctx, http.MethodPost, pipelinesPath(plan.Team.ValueString()), nil, in, nil); err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddError("That pipeline already exists", fmt.Sprintf("Import it: terraform import <address> %s/%s", plan.Team.ValueString(), plan.Name.ValueString()))
			return
		}
		resp.Diagnostics.AddError("Couldn't register the pipeline", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Team.ValueString() + "/" + plan.Name.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *pipelineResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state pipelineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := r.fetch(ctx, state.Team.ValueString(), state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the pipeline", err.Error())
		return
	}
	if got == nil {
		resp.State.RemoveResource(ctx)
		return
	}
	state.ID = types.StringValue(state.Team.ValueString() + "/" + got.Name)
	state.Issuer, state.Repository, state.Ref = types.StringValue(got.Issuer), types.StringValue(got.Repository), connString(got.Ref)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *pipelineResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan pipelineModel // every attribute replaces it, so nothing changes in place
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *pipelineResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state pipelineModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Do(ctx, http.MethodDelete, pipelinesPath(state.Team.ValueString())+"/"+url.PathEscape(state.Name.ValueString()), nil, nil, nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Couldn't remove the pipeline", err.Error())
	}
}

func (r *pipelineResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := splitID(req.ID, 2, "team/name, such as payments/docs-ci", &resp.Diagnostics)
	if parts == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
}
