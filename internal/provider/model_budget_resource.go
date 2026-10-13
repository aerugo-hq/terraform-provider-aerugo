package provider

import (
	"context"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
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
	_ resource.Resource                = &modelBudgetResource{}
	_ resource.ResourceWithConfigure   = &modelBudgetResource{}
	_ resource.ResourceWithImportState = &modelBudgetResource{}
)

// A budget is its own resource because org admins set it, while a team's admins manage the model itself.
type modelBudgetResource struct {
	client *client.Client
}

type modelBudgetModel struct {
	ID            types.String `tfsdk:"id"`
	Team          types.String `tfsdk:"team"`
	Model         types.String `tfsdk:"model"`
	MonthlyTokens types.Int64  `tfsdk:"monthly_tokens"`
}

// NewModelBudgetResource is aerugo_model_budget.
func NewModelBudgetResource() resource.Resource { return &modelBudgetResource{} }

func (r *modelBudgetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model_budget"
}

func (r *modelBudgetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	slug := []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)}
	resp.Schema = schema.Schema{
		Description: "A cap on a team's model, in tokens per calendar month (UTC). Past it, the model answers 429 until the " +
			"month turns. Org admins set it; destroying this removes the cap.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:      true,
				Description:   "team/model, the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"team":  schema.StringAttribute{Required: true, Description: "The team's slug.", PlanModifiers: replace, Validators: slug},
			"model": schema.StringAttribute{Required: true, Description: "The model's name in its team.", PlanModifiers: replace, Validators: slug},
			"monthly_tokens": schema.Int64Attribute{
				Required:    true,
				Description: "Tokens the model may use in a calendar month (UTC), prompts and answers together.",
				Validators:  []validator.Int64{int64validator.Between(1, 1_000_000_000_000)},
			},
		},
	}
}

func (r *modelBudgetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *modelBudgetResource) set(ctx context.Context, m *modelBudgetModel, tokens any) error {
	return r.client.Do(ctx, http.MethodPut, modelPath(m.Team.ValueString(), m.Model.ValueString())+"/budget", nil,
		map[string]any{"monthly_tokens": tokens}, nil)
}

func (r *modelBudgetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan modelBudgetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.set(ctx, &plan, plan.MonthlyTokens.ValueInt64()); err != nil {
		resp.Diagnostics.AddError("Couldn't set the budget", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Team.ValueString() + "/" + plan.Model.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *modelBudgetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state modelBudgetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := fetchModel(ctx, r.client, state.Team.ValueString(), state.Model.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the budget", err.Error())
		return
	}
	if got == nil || got.Budget == nil {
		resp.State.RemoveResource(ctx) // the model is gone, or its cap was removed in the app
		return
	}
	state.ID = types.StringValue(state.Team.ValueString() + "/" + state.Model.ValueString())
	state.MonthlyTokens = types.Int64Value(*got.Budget)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *modelBudgetResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan modelBudgetModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.set(ctx, &plan, plan.MonthlyTokens.ValueInt64()); err != nil {
		resp.Diagnostics.AddError("Couldn't change the budget", err.Error())
		return
	}
	plan.ID = types.StringValue(plan.Team.ValueString() + "/" + plan.Model.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *modelBudgetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state modelBudgetModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.set(ctx, &state, nil); err != nil && !client.IsNotFound(err) { // no number: the cap goes
		resp.Diagnostics.AddError("Couldn't remove the budget", err.Error())
	}
}

func (r *modelBudgetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := splitID(req.ID, 2, "team/model, such as payments/claude", &resp.Diagnostics)
	if parts == nil {
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("team"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("model"), parts[1])...)
}
