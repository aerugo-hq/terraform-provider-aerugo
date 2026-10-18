package provider

import (
	"context"
	"net/http"

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

// Who gets in (G4), the org admins' to set: "org", anyone the identity provider signs in; "teams", only members of an
// onboarded team and org admins (a pilot, rolled out a team at a time). One per org; removing it returns the org to "org".
var (
	_ resource.Resource                = &admissionResource{}
	_ resource.ResourceWithConfigure   = &admissionResource{}
	_ resource.ResourceWithImportState = &admissionResource{}
)

const admissionID = "admission"

type admissionResource struct{ client *client.Client }

type admissionModel struct {
	ID   types.String `tfsdk:"id"`
	Mode types.String `tfsdk:"mode"`
}

// NewAdmissionResource is aerugo_admission.
func NewAdmissionResource() resource.Resource { return &admissionResource{} }

func (r *admissionResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_admission"
}

func (r *admissionResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "Who gets in: \"org\", anyone the identity provider signs in; \"teams\", only members of an onboarded team " +
			"and org admins, for a pilot rolled out a team at a time. Service accounts always get in. One per org; " +
			"removing it returns the org to \"org\". Org admins only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{Computed: true, Description: "Always \"admission\", the form terraform import takes.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"mode": schema.StringAttribute{Required: true, Description: "org or teams. Changes in place.",
				Validators: []validator.String{stringvalidator.OneOf("org", "teams")}},
		},
	}
}

func (r *admissionResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.client = providerClient(req.ProviderData, &resp.Diagnostics)
}

func (r *admissionResource) put(ctx context.Context, mode string) error {
	return r.client.Do(ctx, http.MethodPut, "/v1/admin/admission", nil, map[string]any{"mode": mode}, nil)
}

func (r *admissionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan admissionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, plan.Mode.ValueString()); err != nil {
		resp.Diagnostics.AddError("Couldn't set who gets in", err.Error())
		return
	}
	plan.ID = types.StringValue(admissionID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *admissionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state admissionModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var got struct {
		Mode string `json:"mode"`
	}
	if err := r.client.Do(ctx, http.MethodGet, "/v1/admin/admission", nil, nil, &got); err != nil {
		resp.Diagnostics.AddError("Couldn't read who gets in", err.Error())
		return
	}
	state.ID, state.Mode = types.StringValue(admissionID), types.StringValue(got.Mode)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *admissionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan admissionModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.put(ctx, plan.Mode.ValueString()); err != nil {
		resp.Diagnostics.AddError("Couldn't change who gets in", err.Error())
		return
	}
	plan.ID = types.StringValue(admissionID)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *admissionResource) Delete(ctx context.Context, _ resource.DeleteRequest, resp *resource.DeleteResponse) {
	if err := r.put(ctx, "org"); err != nil { // removed: everyone signed in gets in again, as before a pilot
		resp.Diagnostics.AddError("Couldn't return the org to letting everyone in", err.Error())
	}
}

func (r *admissionResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != admissionID {
		resp.Diagnostics.AddError("Unexpected import id", "aerugo_admission imports as \"admission\"")
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
