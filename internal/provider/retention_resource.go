package provider

import (
	"context"
	"net/http"
	"net/url"

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

// Compliance rules (slice I2): retention policies and legal holds, the org admins' to set. Both are read back from
// GET /v1/retention, which lists every policy and hold.
var (
	_ resource.Resource                = &retentionPolicyResource{}
	_ resource.ResourceWithConfigure   = &retentionPolicyResource{}
	_ resource.ResourceWithImportState = &retentionPolicyResource{}
	_ resource.Resource                = &legalHoldResource{}
	_ resource.ResourceWithConfigure   = &legalHoldResource{}
	_ resource.ResourceWithImportState = &legalHoldResource{}
)

type retentionAPI struct {
	Policies []struct {
		ID             string  `json:"id"`
		Workspace      *string `json:"workspace"`
		Target         string  `json:"target"`
		Classification *string `json:"classification"`
		KeepDays       int64   `json:"keep_days"`
	} `json:"policies"`
	Holds []struct {
		ID         string  `json:"id"`
		Workspace  *string `json:"workspace"`
		Reason     string  `json:"reason"`
		ReleasedAt *string `json:"released_at"`
	} `json:"holds"`
}

func fetchRetention(ctx context.Context, c *client.Client) (*retentionAPI, error) {
	var got retentionAPI
	if err := c.Do(ctx, http.MethodGet, "/v1/retention", nil, nil, &got); err != nil {
		return nil, err
	}
	return &got, nil
}

// ---------------------------------------------------------------- aerugo_retention_policy

type retentionPolicyResource struct{ client *client.Client }

type retentionPolicyModel struct {
	ID             types.String `tfsdk:"id"`
	Team           types.String `tfsdk:"team"`
	Target         types.String `tfsdk:"target"`
	Classification types.String `tfsdk:"classification"`
	KeepDays       types.Int64  `tfsdk:"keep_days"`
}

// NewRetentionPolicyResource is aerugo_retention_policy.
func NewRetentionPolicyResource() resource.Resource { return &retentionPolicyResource{} }

func (r *retentionPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_retention_policy"
}

func (r *retentionPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "Keep a kind of record for a number of days, for the whole org or one team (and, for documents, one " +
			"classification); no policy keeps records forever. A legal hold overrides it. Org admins only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "The policy's id, the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"team": schema.StringAttribute{Optional: true, PlanModifiers: replace,
				Description: "The team's slug; unset, the whole org. Only documents and model_usage are kept per team.",
				Validators:  []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)}},
			"target": schema.StringAttribute{Required: true, PlanModifiers: replace,
				Description: "What it keeps: documents, concept_versions, audit_events (at least 30 days), or model_usage.",
				Validators:  []validator.String{stringvalidator.OneOf("documents", "concept_versions", "audit_events", "model_usage")}},
			"classification": schema.StringAttribute{Optional: true, PlanModifiers: replace,
				Description: "Documents only: just this classification.",
				Validators:  []validator.String{stringvalidator.OneOf("public", "internal", "confidential", "restricted")}},
			"keep_days": schema.Int64Attribute{Required: true, Description: "How many days to keep them. Changes in place.",
				Validators: []validator.Int64{int64validator.Between(1, 36500)}},
		},
	}
}

func (r *retentionPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *retentionPolicyResource) put(ctx context.Context, m *retentionPolicyModel) error {
	in := map[string]any{"target": m.Target.ValueString(), "keep_days": m.KeepDays.ValueInt64()}
	if connSet(m.Team) {
		in["workspace"] = m.Team.ValueString()
	}
	if connSet(m.Classification) {
		in["classification"] = m.Classification.ValueString()
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := r.client.Do(ctx, http.MethodPut, "/v1/retention/policies", nil, in, &out); err != nil {
		return err
	}
	m.ID = types.StringValue(out.ID)
	return nil
}

func (r *retentionPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan retentionPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &plan); err != nil {
		resp.Diagnostics.AddError("Couldn't set the retention policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *retentionPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state retentionPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := fetchRetention(ctx, r.client)
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the retention policies", err.Error())
		return
	}
	for _, p := range got.Policies {
		if p.ID == state.ID.ValueString() {
			state.Team, state.Target, state.Classification = connString(p.Workspace), types.StringValue(p.Target), connString(p.Classification)
			state.KeepDays = types.Int64Value(p.KeepDays)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx) // removed in the app: the next plan sets it again
}

func (r *retentionPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan retentionPolicyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, &plan); err != nil { // the same scope: the API replaces its days in place
		resp.Diagnostics.AddError("Couldn't change the retention policy", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *retentionPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state retentionPolicyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Do(ctx, http.MethodDelete, "/v1/retention/policies/"+url.PathEscape(state.ID.ValueString()), nil, nil, nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Couldn't remove the retention policy", err.Error())
	}
}

func (r *retentionPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ---------------------------------------------------------------- aerugo_legal_hold

type legalHoldResource struct{ client *client.Client }

type legalHoldModel struct {
	ID     types.String `tfsdk:"id"`
	Team   types.String `tfsdk:"team"`
	Reason types.String `tfsdk:"reason"`
}

// NewLegalHoldResource is aerugo_legal_hold.
func NewLegalHoldResource() resource.Resource { return &legalHoldResource{} }

func (r *legalHoldResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_legal_hold"
}

func (r *legalHoldResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		Description: "A legal hold on the whole org or one team: retention removes nothing it covers, and a held team can't " +
			"be purged. Destroying it releases the hold, which stays on record as released. Org admins only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "The hold's id, the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"team": schema.StringAttribute{Optional: true, Description: "The team's slug; unset, the whole org.", PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(slugPattern, "must be "+slugRule)}},
			"reason": schema.StringAttribute{Required: true, Description: "Why it's held, for the record.", PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.LengthBetween(3, 500)}},
		},
	}
}

func (r *legalHoldResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *legalHoldResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan legalHoldModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	in := map[string]any{"reason": plan.Reason.ValueString()}
	if connSet(plan.Team) {
		in["workspace"] = plan.Team.ValueString()
	}
	var out struct {
		ID string `json:"id"`
	}
	if err := r.client.Do(ctx, http.MethodPost, "/v1/legal-holds", nil, in, &out); err != nil {
		resp.Diagnostics.AddError("Couldn't place the legal hold", err.Error())
		return
	}
	plan.ID = types.StringValue(out.ID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *legalHoldResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state legalHoldModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	got, err := fetchRetention(ctx, r.client)
	if err != nil {
		resp.Diagnostics.AddError("Couldn't read the legal holds", err.Error())
		return
	}
	for _, h := range got.Holds {
		if h.ID == state.ID.ValueString() && h.ReleasedAt == nil {
			state.Team, state.Reason = connString(h.Workspace), types.StringValue(h.Reason)
			resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
			return
		}
	}
	resp.State.RemoveResource(ctx) // released in the app: the next plan places it again
}

func (r *legalHoldResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan legalHoldModel // every attribute replaces it
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *legalHoldResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state legalHoldModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	err := r.client.Do(ctx, http.MethodPost, "/v1/legal-holds/"+url.PathEscape(state.ID.ValueString())+"/release", nil, nil, nil)
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Couldn't release the legal hold", err.Error())
	}
}

func (r *legalHoldResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
