package unifi

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	ui "github.com/ubiquiti-community/go-unifi/unifi"
	"github.com/ubiquiti-community/go-unifi/unifi/settings"
)

var (
	_ resource.Resource                = &settingLocaleResource{}
	_ resource.ResourceWithConfigure   = &settingLocaleResource{}
	_ resource.ResourceWithImportState = &settingLocaleResource{}
	_ resource.ResourceWithIdentity    = &settingLocaleResource{}
)

type settingLocaleResource struct {
	client *Client
}

type settingLocaleResourceModel struct {
	ID       types.String `tfsdk:"id"`
	Site     types.String `tfsdk:"site"`
	Timezone types.String `tfsdk:"timezone"`
}

func NewSettingLocaleResource() resource.Resource {
	return &settingLocaleResource{}
}

func (r *settingLocaleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_setting_locale"
}

func (r *settingLocaleResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{Attributes: map[string]identityschema.Attribute{"id": identityschema.StringAttribute{RequiredForImport: true}}}
}

func (r *settingLocaleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Manages the site timezone.", Attributes: map[string]schema.Attribute{
		"id":       schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
		"site":     schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()}},
		"timezone": schema.StringAttribute{Required: true},
	}}
}

func (r *settingLocaleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *Client, got %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *settingLocaleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan settingLocaleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Identity.SetAttribute(ctx, path.Root("id"), plan.ID)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *settingLocaleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state settingLocaleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := state.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	base, setting, err := ui.GetSetting[*settings.Locale](r.client.ApiClient, ctx, site)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Locale Setting", err.Error())
		return
	}
	state.ID = types.StringValue(base.Id)
	state.Site = types.StringValue(site)
	state.Timezone = types.StringValue(setting.Timezone)
	resp.Diagnostics.Append(resp.Identity.SetAttribute(ctx, path.Root("id"), state.ID)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *settingLocaleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan settingLocaleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &plan, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.Identity.SetAttribute(ctx, path.Root("id"), plan.ID)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *settingLocaleResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *settingLocaleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("site"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *settingLocaleResource) apply(ctx context.Context, model *settingLocaleResourceModel, diags *diag.Diagnostics) {
	site := model.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	base, setting, err := ui.GetSetting[*settings.Locale](r.client.ApiClient, ctx, site)
	if err != nil {
		diags.AddError("Error Reading Locale Setting", err.Error())
		return
	}
	setting.Timezone = model.Timezone.ValueString()
	if err := r.client.UpdateSetting(ctx, site, setting); err != nil {
		diags.AddError("Error Updating Locale Setting", err.Error())
		return
	}
	model.ID = types.StringValue(base.Id)
	model.Site = types.StringValue(site)
}
