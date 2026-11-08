package unifi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type protectCamera struct {
	ID                string `json:"id"`
	MAC               string `json:"mac"`
	Name              string `json:"name"`
	Host              string `json:"host"`
	Adopted           bool   `json:"isAdopted"`
	AdoptedByOther    bool   `json:"isAdoptedByOther"`
	CanAdopt          bool   `json:"canAdopt"`
	RecordingDisabled bool   `json:"-"`
	RecordingSettings struct {
		Mode string `json:"mode"`
	} `json:"recordingSettings"`
	LEDSettings struct {
		IsEnabled bool `json:"isEnabled"`
	} `json:"ledSettings"`
}

func normalizeProtectMAC(mac string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(mac, ":", ""), "-", ""))
}

func (c *consoleClient) camera(ctx context.Context, mac string) (*protectCamera, error) {
	var data struct {
		Cameras []protectCamera `json:"cameras"`
		NVR     struct {
			RecordingDisabled bool `json:"isRecordingDisabled"`
		} `json:"nvr"`
	}
	if err := c.request(ctx, http.MethodGet, "/proxy/protect/api/bootstrap", nil, &data); err != nil {
		return nil, err
	}
	for _, camera := range data.Cameras {
		if normalizeProtectMAC(camera.MAC) == normalizeProtectMAC(mac) {
			camera.RecordingDisabled = data.NVR.RecordingDisabled
			return &camera, nil
		}
	}
	return nil, nil
}

func (c *consoleClient) ensureCamera(ctx context.Context, mac, name, mode, bootstrapHost string, statusLED *bool) (*protectCamera, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	camera, err := c.camera(ctx, mac)
	if err != nil {
		return nil, err
	}
	if camera == nil && bootstrapHost != "" {
		if err := c.bootstrapCamera(ctx, bootstrapHost, mac); err != nil {
			return nil, err
		}
	}
	err = waitConsole(ctx, 5*time.Second, func() (bool, error) {
		var err error
		camera, err = c.camera(ctx, mac)
		return camera != nil, err
	})
	if err != nil {
		return nil, fmt.Errorf("discover Protect camera %s: %w", mac, err)
	}
	if camera.AdoptedByOther {
		return nil, fmt.Errorf("camera %s belongs to another Protect console; refusing to take ownership", mac)
	}
	if mode != "never" && camera.RecordingDisabled {
		return nil, fmt.Errorf("Protect has recording disabled; configure recording storage and enable recording, or set recording_mode to never for live view only")
	}
	if !camera.Adopted {
		if !camera.CanAdopt {
			return nil, fmt.Errorf("camera %s is not adoptable; check camera and console setup in Protect", mac)
		}
		var result struct {
			Cameras map[string]struct {
				Adopted bool `json:"adopted"`
			} `json:"cameras"`
		}
		err := c.request(ctx, http.MethodPost, "/proxy/protect/api/devices/adopt", map[string]any{"cameras": map[string]any{camera.ID: map[string]any{}}}, &result)
		if err != nil {
			return nil, err
		}
		if !result.Cameras[camera.ID].Adopted {
			return nil, fmt.Errorf("Protect did not accept adoption of camera %s", mac)
		}
		err = waitConsole(ctx, 5*time.Second, func() (bool, error) {
			var err error
			camera, err = c.camera(ctx, mac)
			if camera != nil && camera.AdoptedByOther {
				return false, fmt.Errorf("camera %s was adopted by another console", mac)
			}
			return camera != nil && camera.Adopted, err
		})
		if err != nil {
			return nil, err
		}
	}
	update := map[string]any{}
	if camera.Name != name {
		update["name"] = name
	}
	if camera.RecordingSettings.Mode != mode {
		update["recordingSettings"] = map[string]any{"mode": mode}
	}
	if statusLED != nil && camera.LEDSettings.IsEnabled != *statusLED {
		update["ledSettings"] = map[string]any{"isEnabled": *statusLED}
	}
	if len(update) > 0 {
		err := c.request(ctx, http.MethodPatch, "/proxy/protect/api/cameras/"+url.PathEscape(camera.ID), update, nil)
		if err != nil {
			return nil, err
		}
	}
	err = waitConsole(ctx, 2*time.Second, func() (bool, error) {
		var err error
		camera, err = c.camera(ctx, mac)
		return camera != nil && camera.Adopted && !camera.AdoptedByOther && camera.Name == name && camera.RecordingSettings.Mode == mode && (statusLED == nil || camera.LEDSettings.IsEnabled == *statusLED), err
	})
	return camera, err
}

// protectIntegrationCamera is the camera in the official Protect integration
// API. It lists adopted cameras only and does not expose recording settings or
// the camera address.
type protectIntegrationCamera struct {
	ID          string `json:"id"`
	MAC         string `json:"mac"`
	Name        string `json:"name"`
	LEDSettings struct {
		IsEnabled bool `json:"isEnabled"`
	} `json:"ledSettings"`
}

const protectIntegrationCameras = "/proxy/protect/integration/v1/cameras"

func (c *consoleClient) integrationCamera(ctx context.Context, mac string) (*protectIntegrationCamera, error) {
	var cameras []protectIntegrationCamera
	status, err := c.keyRequest(ctx, http.MethodGet, protectIntegrationCameras, nil, &cameras)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Protect integration API returned HTTP %d; check that Protect is running", status)
	}
	for _, camera := range cameras {
		if normalizeProtectMAC(camera.MAC) == normalizeProtectMAC(mac) {
			return &camera, nil
		}
	}
	return nil, nil
}

// updateIntegrationCamera sets the name and status LED of an adopted camera
// with the API key.
func (c *consoleClient) updateIntegrationCamera(ctx context.Context, mac, name string, statusLED *bool) (*protectIntegrationCamera, error) {
	camera, err := c.integrationCamera(ctx, mac)
	if err != nil {
		return nil, err
	}
	if camera == nil {
		return nil, fmt.Errorf("camera %s is not adopted; adoption requires local admin UNIFI_USERNAME and UNIFI_PASSWORD", mac)
	}
	update := map[string]any{}
	if camera.Name != name {
		update["name"] = name
	}
	if statusLED != nil && camera.LEDSettings.IsEnabled != *statusLED {
		update["ledSettings"] = map[string]any{"isEnabled": *statusLED}
	}
	if len(update) == 0 {
		return camera, nil
	}
	var updated protectIntegrationCamera
	status, err := c.keyRequest(ctx, http.MethodPatch, protectIntegrationCameras+"/"+url.PathEscape(camera.ID), update, &updated)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("Protect integration API rejected the camera update with HTTP %d", status)
	}
	if updated.Name != name || (statusLED != nil && updated.LEDSettings.IsEnabled != *statusLED) {
		return nil, fmt.Errorf("Protect did not apply the update to camera %s", mac)
	}
	return &updated, nil
}

type protectCameraResource struct{ client *consoleClient }
type protectCameraModel struct {
	ID               types.String `tfsdk:"id"`
	MAC              types.String `tfsdk:"mac"`
	Name             types.String `tfsdk:"name"`
	RecordingMode    types.String `tfsdk:"recording_mode"`
	StatusLED        types.Bool   `tfsdk:"status_led"`
	Host             types.String `tfsdk:"host"`
	BootstrapHost    types.String `tfsdk:"bootstrap_host"`
	UnadoptOnDestroy types.Bool   `tfsdk:"unadopt_on_destroy"`
}

func NewProtectCameraResource() resource.Resource { return &protectCameraResource{} }
func (r *protectCameraResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_protect_camera"
}
func (r *protectCameraResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Adopts a camera into UniFi Protect and manages its name, recording mode and status LED. Requires Protect installed and running. Adoption, recording mode changes and unadoption need a local admin session (`UNIFI_USERNAME` and `UNIFI_PASSWORD`). Without one, the provider reads and updates the name and status LED of an adopted camera through the Protect integration API with the API key; `recording_mode` and `host` then keep their last known values. Never takes over cameras belonging to another console. Cameras remain adopted on destroy by default.",
		Attributes: map[string]schema.Attribute{
			"id":                 schema.StringAttribute{Computed: true, PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()}},
			"mac":                schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.RegexMatches(regexp.MustCompile(`^[0-9a-fA-F]{2}(:[0-9a-fA-F]{2}){5}$`), "must be a colon-separated MAC address")}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}},
			"name":               schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.LengthAtLeast(1)}},
			"recording_mode":     schema.StringAttribute{Required: true, Validators: []validator.String{stringvalidator.OneOf("always", "detections", "never")}},
			"status_led":         schema.BoolAttribute{Optional: true, Computed: true, MarkdownDescription: "Enable the camera status LED. When omitted, preserves the current setting. Does not control infrared illumination or other LED settings."},
			"host":               schema.StringAttribute{Computed: true},
			"bootstrap_host":     schema.StringAttribute{Optional: true, MarkdownDescription: "Initial camera IP for configuring its Protect server if discovery has not found it. Uses factory credentials or UNIFI_CAMERA_USERNAME and UNIFI_CAMERA_PASSWORD. Camera credentials are never retained in state."},
			"unadopt_on_destroy": schema.BoolAttribute{Optional: true, Computed: true, Default: booldefault.StaticBool(false), MarkdownDescription: "Unadopt the camera on destroy; defaults to false to preserve surveillance."},
		},
	}
}
func (r *protectCameraResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *Client, got %T", req.ProviderData))
		return
	}
	r.client = client.console
}

// apply converges the camera. prior is the current state, or nil on create.
func (r *protectCameraResource) apply(ctx context.Context, model, prior *protectCameraModel, diags *diag.Diagnostics) {
	var statusLED *bool
	if !model.StatusLED.IsNull() && !model.StatusLED.IsUnknown() {
		value := model.StatusLED.ValueBool()
		statusLED = &value
	}
	if !r.client.hasSession() {
		if prior == nil || !prior.RecordingMode.Equal(model.RecordingMode) {
			diags.AddError("Error Managing Protect Camera", "Adopting a camera or changing recording_mode requires local admin UNIFI_USERNAME and UNIFI_PASSWORD.")
			return
		}
		camera, err := r.client.updateIntegrationCamera(ctx, model.MAC.ValueString(), model.Name.ValueString(), statusLED)
		if err != nil {
			diags.AddError("Error Managing Protect Camera", err.Error())
			return
		}
		model.ID = types.StringValue(camera.ID)
		model.Host = prior.Host
		model.StatusLED = types.BoolValue(camera.LEDSettings.IsEnabled)
		return
	}
	camera, err := r.client.ensureCamera(ctx, model.MAC.ValueString(), model.Name.ValueString(), model.RecordingMode.ValueString(), model.BootstrapHost.ValueString(), statusLED)
	if err != nil {
		diags.AddError("Error Managing Protect Camera", err.Error())
		return
	}
	model.ID = types.StringValue(camera.ID)
	model.Host = types.StringValue(camera.Host)
	model.StatusLED = types.BoolValue(camera.LEDSettings.IsEnabled)
}
func (r *protectCameraResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model protectCameraModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &model, nil, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}
func (r *protectCameraResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model, prior protectCameraModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &model, &prior, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	}
}
func (r *protectCameraResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model protectCameraModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !r.client.hasSession() {
		camera, err := r.client.integrationCamera(ctx, model.MAC.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error Reading Protect Camera", err.Error())
			return
		}
		if camera == nil {
			resp.State.RemoveResource(ctx)
			return
		}
		model.ID = types.StringValue(camera.ID)
		model.Name = types.StringValue(camera.Name)
		model.StatusLED = types.BoolValue(camera.LEDSettings.IsEnabled)
		resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
		return
	}
	camera, err := r.client.camera(ctx, model.MAC.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Protect Camera", err.Error())
		return
	}
	if camera != nil && camera.AdoptedByOther {
		resp.Diagnostics.AddError("Camera Owned by Another Console", "Refusing to manage this camera. Restore its ownership in Protect before applying.")
		return
	}
	if camera == nil || !camera.Adopted {
		resp.State.RemoveResource(ctx)
		return
	}
	model.ID = types.StringValue(camera.ID)
	model.Name = types.StringValue(camera.Name)
	model.RecordingMode = types.StringValue(camera.RecordingSettings.Mode)
	model.Host = types.StringValue(camera.Host)
	model.StatusLED = types.BoolValue(camera.LEDSettings.IsEnabled)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
func (r *protectCameraResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model protectCameraModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() || !model.UnadoptOnDestroy.ValueBool() {
		return
	}
	camera, err := r.client.camera(ctx, model.MAC.ValueString())
	if err == nil && camera != nil && camera.Adopted && !camera.AdoptedByOther {
		err = r.client.request(ctx, http.MethodDelete, "/proxy/protect/api/cameras/"+url.PathEscape(camera.ID), nil, nil)
		if err == nil {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			defer cancel()
			err = waitConsole(ctx, 5*time.Second, func() (bool, error) {
				current, err := r.client.camera(ctx, model.MAC.ValueString())
				return current == nil || !current.Adopted, err
			})
		}
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Unadopting Protect Camera", err.Error())
	}
}
func (r *protectCameraResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("mac"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("unadopt_on_destroy"), false)...)
}
