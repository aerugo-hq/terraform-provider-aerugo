package provider

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/aerugo-hq/terraform-provider-aerugo/internal/client"
)

var (
	_ resource.Resource                = &modelResource{}
	_ resource.ResourceWithConfigure   = &modelResource{}
	_ resource.ResourceWithImportState = &modelResource{}
)

var classifications = []string{"public", "internal", "confidential", "restricted"}

type modelResource struct {
	client *client.Client
}

// The schema calls the API's "provider" field api: provider is one of Terraform's own words in a resource.
type modelModel struct {
	ID                     types.String `tfsdk:"id"`
	Team                   types.String `tfsdk:"team"`
	Name                   types.String `tfsdk:"name"`
	API                    types.String `tfsdk:"api"`
	Model                  types.String `tfsdk:"model"`
	BaseURL                types.String `tfsdk:"base_url"`
	AllowedClassifications types.Set    `tfsdk:"allowed_classifications"`
	APIKey                 types.String `tfsdk:"api_key"`
	APIKeyVersion          types.Int64  `tfsdk:"api_key_version"`
}

// modelAPI is one entry of a team's models list, which is also where a model's budget is read.
type modelAPI struct {
	Name     string   `json:"name"`
	Provider string   `json:"provider"`
	Model    string   `json:"model"`
	BaseURL  *string  `json:"base_url"`
	Allowed  []string `json:"allowed_classifications"`
	Budget   *int64   `json:"monthly_token_budget"`
}

// fetchModel finds a team's model by name: nil if it, or the team, is gone or out of reach.
func fetchModel(ctx context.Context, c *client.Client, team, name string) (*modelAPI, error) {
	var got struct {
		Models []modelAPI `json:"models"`
	}
	err := c.Do(ctx, http.MethodGet, "/v1/workspaces/"+url.PathEscape(team)+"/models", nil, nil, &got)
	if client.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	for i := range got.Models {
		if got.Models[i].Name == name {
			return &got.Models[i], nil
		}
	}
	return nil, nil
}

func modelPath(team, name string) string {
	return "/v1/workspaces/" + url.PathEscape(team) + "/models/" + url.PathEscape(name)
}

// splitID parses an import ID of n slash-separated parts, the form each resource documents.
func splitID(id string, n int, form string, diags *diag.Diagnostics) []string {
	parts := strings.SplitN(id, "/", n)
	if len(parts) != n {
		diags.AddError("Unexpected import ID", fmt.Sprintf("Expected %s; got %q.", form, id))
		return nil
	}
	for _, p := range parts {
		if p == "" {
			diags.AddError("Unexpected import ID", fmt.Sprintf("Expected %s; got %q.", form, id))
			return nil
		}
	}
	return parts
}

// NewModelResource is aerugo_model.
func NewModelResource() resource.Resource { return &modelResource{} }

func (r *modelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model"
}

func (r *modelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A model a team can ask with. Its key is a write-only argument: Terraform sends it, and never " +
			"stores it in state. Aerugo keeps it in its secrets backend and never returns it. Needs Terraform 1.11 or later.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "team/name, the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"team": schema.StringAttribute{
				Required: true, Description: "The team's slug.", PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)},
			},
			"name": schema.StringAttribute{
				Required: true, Description: "The model's name in its team (" + slugRule + "): people ask with team/name.",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)},
			},
			"api": schema.StringAttribute{
				Required:      true,
				Description:   "Which API the model speaks: anthropic, or openai_compatible (OpenAI's, which many services speak).",
				PlanModifiers: replace,
				Validators:    []validator.String{stringvalidator.OneOf("anthropic", "openai_compatible")},
			},
			"model": schema.StringAttribute{
				Required:    true,
				Description: "The model's own name at its provider, such as claude-sonnet-4-5.",
				Validators:  []validator.String{stringvalidator.LengthBetween(1, 200)},
			},
			"base_url": schema.StringAttribute{
				Optional:    true,
				Description: "Where the model's API is, for a gateway or a self-hosted model. The deployment must allow it.",
				Validators:  []validator.String{stringvalidator.LengthBetween(1, 500)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplaceIf(
					func(_ context.Context, req planmodifier.StringRequest, resp *stringplanmodifier.RequiresReplaceIfFuncResponse) {
						resp.RequiresReplace = !req.StateValue.IsNull() && req.PlanValue.IsNull()
					},
					"Removing base_url registers the model again: the API can change its address, but not remove it.",
					"Removing `base_url` registers the model again: the API can change its address, but not remove it.",
				)},
			},
			"allowed_classifications": schema.SetAttribute{
				Optional:      true,
				Computed:      true,
				ElementType:   types.StringType,
				Description:   "The document classifications this model may receive (egress): public, internal, confidential, restricted. Aerugo's default is public and internal.",
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(classifications...)),
				},
			},
			"api_key": schema.StringAttribute{
				Optional:    true,
				Sensitive:   true,
				WriteOnly:   true,
				Description: "The model's key, needed to register it. Write-only: pass it from an ephemeral, sensitive variable.",
				Validators:  []validator.String{stringvalidator.LengthBetween(1, 8192)},
			},
			"api_key_version": schema.Int64Attribute{
				Optional:    true,
				Description: "Change this to send api_key again: Aerugo replaces the key and destroys every earlier version.",
			},
		},
	}
}

func (r *modelResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

// classesOf is the set as a list for the API, or nil when it isn't set (so the API keeps its own default).
func classesOf(ctx context.Context, set types.Set, diags *diag.Diagnostics) []string {
	if set.IsNull() || set.IsUnknown() {
		return nil
	}
	var out []string
	diags.Append(set.ElementsAs(ctx, &out, false)...)
	return out
}

// apiKey is the write-only key, which only the configuration holds: never the plan, never the state.
func apiKey(ctx context.Context, cfg interface {
	GetAttribute(context.Context, path.Path, any) diag.Diagnostics
}, diags *diag.Diagnostics) string {
	var key types.String
	diags.Append(cfg.GetAttribute(ctx, path.Root("api_key"), &key)...)
	if key.IsNull() || key.IsUnknown() {
		return ""
	}
	return key.ValueString()
}

// refresh copies what the API holds into the model's Terraform state, keeping the key out of it.
func (r *modelResource) refresh(ctx context.Context, m *modelModel, got *modelAPI, diags *diag.Diagnostics) {
	m.ID = types.StringValue(m.Team.ValueString() + "/" + got.Name)
	m.Name = types.StringValue(got.Name)
	m.API = types.StringValue(got.Provider)
	m.Model = types.StringValue(got.Model)
	if got.BaseURL != nil && *got.BaseURL != "" {
		m.BaseURL = types.StringValue(*got.BaseURL)
	} else {
		m.BaseURL = types.StringNull()
	}
	allowed, d := types.SetValueFrom(ctx, types.StringType, got.Allowed)
	diags.Append(d...)
	m.AllowedClassifications = allowed
	m.APIKey = types.StringNull()
}

func (r *modelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan modelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	key := apiKey(ctx, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	if key == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing api_key",
			"A model needs its key to be registered. Pass it from an ephemeral, sensitive variable, so it's never stored.")
		return
	}
	team, name := plan.Team.ValueString(), plan.Name.ValueString()
	in := map[string]any{"name": name, "provider": plan.API.ValueString(), "model": plan.Model.ValueString(), "api_key": key}
	if !plan.BaseURL.IsNull() && !plan.BaseURL.IsUnknown() {
		in["base_url"] = plan.BaseURL.ValueString()
	}
	if classes := classesOf(ctx, plan.AllowedClassifications, &resp.Diagnostics); classes != nil {
		in["allowed_classifications"] = classes
	}
	if err := r.client.Do(ctx, http.MethodPost, "/v1/workspaces/"+url.PathEscape(team)+"/models", nil, in, nil); err != nil {
		if client.IsConflict(err) {
			resp.Diagnostics.AddError("That model already exists",
				fmt.Sprintf("Team %s already has a model named %q. To manage it with Terraform, import it: terraform import <address> %s/%s", team, name, team, name))
			return
		}
		resp.Diagnostics.AddError("Couldn't register the model", err.Error())
		return
	}
	got, err := fetchModel(ctx, r.client, team, name)
	if err != nil || got == nil {
		resp.Diagnostics.AddError("Registered the model, but couldn't read it back", fmt.Sprint(err))
		return
	}
	r.refresh(ctx, &plan, got, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *modelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state modelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := fetchModel(ctx, r.client, state.Team.ValueString(), state.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the model", err.Error())
		return
	}
	if got == nil {
		resp.State.RemoveResource(ctx) // removed in the app: the next plan offers to register it again
		return
	}
	r.refresh(ctx, &state, got, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *modelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state modelModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := map[string]any{"model": plan.Model.ValueString()}
	if !plan.BaseURL.IsNull() && !plan.BaseURL.IsUnknown() {
		in["base_url"] = plan.BaseURL.ValueString()
	}
	if classes := classesOf(ctx, plan.AllowedClassifications, &resp.Diagnostics); classes != nil {
		in["allowed_classifications"] = classes
	}
	if !plan.APIKeyVersion.Equal(state.APIKeyVersion) { // a rotation: send the key again, only then
		key := apiKey(ctx, req.Config, &resp.Diagnostics)
		if key == "" {
			resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing api_key",
				"api_key_version changed, which replaces the model's key, but no api_key was given.")
			return
		}
		in["api_key"] = key
	}
	team, name := plan.Team.ValueString(), plan.Name.ValueString()
	if err := r.client.Do(ctx, http.MethodPatch, modelPath(team, name), nil, in, nil); err != nil {
		resp.Diagnostics.AddError("Couldn't update the model", err.Error())
		return
	}
	got, err := fetchModel(ctx, r.client, team, name)
	if err != nil || got == nil {
		resp.Diagnostics.AddError("Updated the model, but couldn't read it back", fmt.Sprint(err))
		return
	}
	r.refresh(ctx, &plan, got, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *modelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state modelModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Do(ctx, http.MethodDelete, modelPath(state.Team.ValueString(), state.Name.ValueString()), nil, nil, nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Couldn't remove the model", err.Error())
	}
}

func (r *modelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := splitID(req.ID, 2, "team/name, such as payments/claude", &resp.Diagnostics)
	if parts == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("name"), parts[1])...)
}
