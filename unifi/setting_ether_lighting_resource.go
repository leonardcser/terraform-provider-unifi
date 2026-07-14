package unifi

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	ui "github.com/ubiquiti-community/go-unifi/unifi"
	"github.com/ubiquiti-community/go-unifi/unifi/settings"
)

var (
	_ resource.Resource                = &settingEtherLightingResource{}
	_ resource.ResourceWithConfigure   = &settingEtherLightingResource{}
	_ resource.ResourceWithImportState = &settingEtherLightingResource{}
	_ resource.ResourceWithIdentity    = &settingEtherLightingResource{}
)

type settingEtherLightingResource struct {
	client *Client
}

type settingEtherLightingResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Site             types.String `tfsdk:"site"`
	NetworkOverrides types.Set    `tfsdk:"network_overrides"`
	SpeedOverrides   types.Set    `tfsdk:"speed_overrides"`
}

type settingEtherLightingNetworkOverrideModel struct {
	NetworkID types.String `tfsdk:"network_id"`
	ColorHex  types.String `tfsdk:"color_hex"`
}

type settingEtherLightingSpeedOverrideModel struct {
	Speed    types.String `tfsdk:"speed"`
	ColorHex types.String `tfsdk:"color_hex"`
}

var (
	settingEtherLightingNetworkOverrideAttrTypes = map[string]attr.Type{
		"network_id": types.StringType,
		"color_hex":  types.StringType,
	}
	settingEtherLightingSpeedOverrideAttrTypes = map[string]attr.Type{
		"speed":     types.StringType,
		"color_hex": types.StringType,
	}
)

func NewSettingEtherLightingResource() resource.Resource {
	return &settingEtherLightingResource{}
}

func (r *settingEtherLightingResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_setting_ether_lighting"
}

func (r *settingEtherLightingResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{Attributes: map[string]identityschema.Attribute{"id": identityschema.StringAttribute{RequiredForImport: true}}}
}

func (r *settingEtherLightingResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	colorValidator := stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9A-Fa-f]{6}$`), "must be a six-digit RGB hex value without #")
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the site-level Etherlighting color palette.",
		Attributes: map[string]schema.Attribute{
			"id":   schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"site": schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()}},
			"network_overrides": schema.SetNestedAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"network_id": schema.StringAttribute{Required: true},
					"color_hex":  schema.StringAttribute{Required: true, Validators: []validator.String{colorValidator}},
				}},
			},
			"speed_overrides": schema.SetNestedAttribute{
				Required:      true,
				PlanModifiers: []planmodifier.Set{setplanmodifier.UseStateForUnknown()},
				NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
					"speed":     schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("FE", "GbE", "2.5GbE", "5GbE", "10GbE", "25GbE", "40GbE", "100GbE")}},
					"color_hex": schema.StringAttribute{Required: true, Validators: []validator.String{colorValidator}},
				}},
			},
		},
	}
}

func (r *settingEtherLightingResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *settingEtherLightingResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan settingEtherLightingResourceModel
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

func (r *settingEtherLightingResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state settingEtherLightingResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := state.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	base, setting, err := ui.GetSetting[*settings.EtherLighting](r.client.ApiClient, ctx, site)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Etherlighting Setting", err.Error())
		return
	}
	state.ID = types.StringValue(base.Id)
	state.Site = types.StringValue(site)
	state.NetworkOverrides = networkOverridesToSet(ctx, setting.NetworkOverrides, &resp.Diagnostics)
	state.SpeedOverrides = speedOverridesToSet(ctx, setting.SpeedOverrides, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.Identity.SetAttribute(ctx, path.Root("id"), state.ID)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *settingEtherLightingResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan settingEtherLightingResourceModel
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

func (r *settingEtherLightingResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *settingEtherLightingResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("site"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *settingEtherLightingResource) apply(ctx context.Context, model *settingEtherLightingResourceModel, diags *diag.Diagnostics) {
	site := model.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	base, setting, err := ui.GetSetting[*settings.EtherLighting](r.client.ApiClient, ctx, site)
	if err != nil {
		diags.AddError("Error Reading Etherlighting Setting", err.Error())
		return
	}
	var networkOverrides []settingEtherLightingNetworkOverrideModel
	diags.Append(model.NetworkOverrides.ElementsAs(ctx, &networkOverrides, false)...)
	var speedOverrides []settingEtherLightingSpeedOverrideModel
	diags.Append(model.SpeedOverrides.ElementsAs(ctx, &speedOverrides, false)...)
	if diags.HasError() {
		return
	}
	setting.NetworkOverrides = make([]settings.SettingEtherLightingNetworkOverrides, 0, len(networkOverrides))
	for _, override := range networkOverrides {
		setting.NetworkOverrides = append(setting.NetworkOverrides, settings.SettingEtherLightingNetworkOverrides{Key: override.NetworkID.ValueString(), RawColorHex: override.ColorHex.ValueString()})
	}
	setting.SpeedOverrides = make([]settings.SettingEtherLightingSpeedOverrides, 0, len(speedOverrides))
	for _, override := range speedOverrides {
		setting.SpeedOverrides = append(setting.SpeedOverrides, settings.SettingEtherLightingSpeedOverrides{Key: override.Speed.ValueString(), RawColorHex: override.ColorHex.ValueString()})
	}
	if err := r.client.UpdateSetting(ctx, site, setting); err != nil {
		diags.AddError("Error Updating Etherlighting Setting", err.Error())
		return
	}
	model.ID = types.StringValue(base.Id)
	model.Site = types.StringValue(site)
}

func networkOverridesToSet(ctx context.Context, overrides []settings.SettingEtherLightingNetworkOverrides, diags *diag.Diagnostics) types.Set {
	models := make([]settingEtherLightingNetworkOverrideModel, 0, len(overrides))
	for _, override := range overrides {
		models = append(models, settingEtherLightingNetworkOverrideModel{NetworkID: types.StringValue(override.Key), ColorHex: types.StringValue(override.RawColorHex)})
	}
	value, valueDiags := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: settingEtherLightingNetworkOverrideAttrTypes}, models)
	diags.Append(valueDiags...)
	return value
}

func speedOverridesToSet(ctx context.Context, overrides []settings.SettingEtherLightingSpeedOverrides, diags *diag.Diagnostics) types.Set {
	models := make([]settingEtherLightingSpeedOverrideModel, 0, len(overrides))
	for _, override := range overrides {
		models = append(models, settingEtherLightingSpeedOverrideModel{Speed: types.StringValue(override.Key), ColorHex: types.StringValue(override.RawColorHex)})
	}
	value, valueDiags := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: settingEtherLightingSpeedOverrideAttrTypes}, models)
	diags.Append(valueDiags...)
	return value
}
