package unifi

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	ui "github.com/ubiquiti-community/go-unifi/unifi"
	"github.com/ubiquiti-community/go-unifi/unifi/settings"
)

var (
	_ resource.Resource                = &settingWifiSpeedResource{}
	_ resource.ResourceWithConfigure   = &settingWifiSpeedResource{}
	_ resource.ResourceWithImportState = &settingWifiSpeedResource{}
	_ resource.ResourceWithIdentity    = &settingWifiSpeedResource{}
)

type settingWifiSpeedResource struct {
	client *Client
}

type settingWifiSpeedResourceModel struct {
	ID           types.String `tfsdk:"id"`
	Site         types.String `tfsdk:"site"`
	Preset       types.String `tfsdk:"preset"`
	Width2GHz    types.Int64  `tfsdk:"width_2_4_ghz"`
	Width5GHz    types.Int64  `tfsdk:"width_5_ghz"`
	Width6GHz    types.Int64  `tfsdk:"width_6_ghz"`
	Extended5GHz types.Bool   `tfsdk:"extended_5_ghz_spectrum"`
}

func NewSettingWifiSpeedResource() resource.Resource {
	return &settingWifiSpeedResource{}
}

func (r *settingWifiSpeedResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_setting_wifi_speed"
}

func (r *settingWifiSpeedResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{Attributes: map[string]identityschema.Attribute{"id": identityschema.StringAttribute{RequiredForImport: true}}}
}

func (r *settingWifiSpeedResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the site-level Default WiFi Speeds preset and channel widths.",
		Attributes: map[string]schema.Attribute{
			"id":                      schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"site":                    schema.StringAttribute{Optional: true, Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace(), stringplanmodifier.UseStateForUnknown()}},
			"preset":                  schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("maximum_speed", "conservative", "custom")}},
			"width_2_4_ghz":           schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.OneOf(20, 40)}},
			"width_5_ghz":             schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.OneOf(20, 40, 80, 160)}},
			"width_6_ghz":             schema.Int64Attribute{Required: true, Validators: []validator.Int64{int64validator.OneOf(20, 40, 80, 160, 320)}},
			"extended_5_ghz_spectrum": schema.BoolAttribute{Required: true},
		},
	}
}

func (r *settingWifiSpeedResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *settingWifiSpeedResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan settingWifiSpeedResourceModel
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

func (r *settingWifiSpeedResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state settingWifiSpeedResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	site := state.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	base, setting, err := ui.GetSetting[*settings.RadioAi](r.client.ApiClient, ctx, site)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading WiFi Speed Setting", err.Error())
		return
	}
	widths, err := wifiRadioWidths(setting.RadiosConfiguration)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading WiFi Speed Setting", err.Error())
		return
	}
	state.ID = types.StringValue(base.Id)
	state.Site = types.StringValue(site)
	state.Preset = types.StringValue(setting.AutoChannelPresetsType)
	state.Width2GHz = types.Int64Value(widths["ng"])
	state.Width5GHz = types.Int64Value(widths["na"])
	state.Width6GHz = types.Int64Value(widths["6e"])
	for _, radio := range setting.RadiosConfiguration {
		if radio.Radio == "na" {
			state.Extended5GHz = types.BoolValue(radio.Dfs)
			break
		}
	}
	resp.Diagnostics.Append(resp.Identity.SetAttribute(ctx, path.Root("id"), state.ID)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *settingWifiSpeedResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan settingWifiSpeedResourceModel
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

func (r *settingWifiSpeedResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
}

func (r *settingWifiSpeedResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("site"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func (r *settingWifiSpeedResource) apply(ctx context.Context, model *settingWifiSpeedResourceModel, diags *diag.Diagnostics) {
	site := model.Site.ValueString()
	if site == "" {
		site = r.client.Site
	}
	base, setting, err := ui.GetSetting[*settings.RadioAi](r.client.ApiClient, ctx, site)
	if err != nil {
		diags.AddError("Error Reading WiFi Speed Setting", err.Error())
		return
	}
	setting.AutoChannelPresetsType = model.Preset.ValueString()
	desired := map[string]int64{
		"ng": model.Width2GHz.ValueInt64(),
		"na": model.Width5GHz.ValueInt64(),
		"6e": model.Width6GHz.ValueInt64(),
	}
	seen := make(map[string]bool, len(desired))
	for index := range setting.RadiosConfiguration {
		radio := setting.RadiosConfiguration[index].Radio
		width, managed := desired[radio]
		if !managed {
			continue
		}
		setting.RadiosConfiguration[index].ChannelWidth = &width
		if radio == "na" {
			setting.RadiosConfiguration[index].Dfs = model.Extended5GHz.ValueBool()
		}
		seen[radio] = true
	}
	for _, radio := range []string{"ng", "na", "6e"} {
		if seen[radio] {
			continue
		}
		width := desired[radio]
		setting.RadiosConfiguration = append(setting.RadiosConfiguration, settings.SettingRadioAiRadiosConfiguration{
			Radio: radio, ChannelWidth: &width, Dfs: radio == "na" && model.Extended5GHz.ValueBool(),
		})
	}
	if err := r.client.UpdateSetting(ctx, site, setting); err != nil {
		diags.AddError("Error Updating WiFi Speed Setting", err.Error())
		return
	}
	model.ID = types.StringValue(base.Id)
	model.Site = types.StringValue(site)
}

func wifiRadioWidths(configuration []settings.SettingRadioAiRadiosConfiguration) (map[string]int64, error) {
	widths := make(map[string]int64, len(configuration))
	for _, radio := range configuration {
		if radio.ChannelWidth != nil {
			widths[radio.Radio] = *radio.ChannelWidth
		}
	}
	for _, radio := range []string{"ng", "na", "6e"} {
		if _, ok := widths[radio]; !ok {
			return nil, fmt.Errorf("controller did not return a channel width for %s", radio)
		}
	}
	return widths, nil
}
