package api

import "time"

// APAction names an AP action (POST /api/aps/$id/actions/$action).
type APAction string

const (
	APActionLocate        APAction = "locate"
	APActionUnlocate      APAction = "unlocate"
	APActionPushConfig    APAction = "push-config"
	APActionReboot        APAction = "reboot"
	APActionRestartRadios APAction = "restart-radios"
)

// ActionInput is the body of an AP or client action (all optional).
type ActionInput struct {
	// Minutes is the locate duration: 1, 5, 15 or 60 (default 5).
	Minutes int `json:"minutes,omitempty"`
	// Band is "2.4", "5" or "" (both) for restart-radios.
	Band string `json:"band,omitempty"`
	// Note is the blocked-client note.
	Note string `json:"note,omitempty"`
}

// ActionResult is the response of an action.
type ActionResult struct {
	Action   string     `json:"action"`
	State    string     `json:"state" tstype:"'accepted' | 'done'"`
	Until    *time.Time `json:"until,omitempty"`
	ActionID string     `json:"action_id,omitempty"`
	// Via says how the action was carried out: "ap-cmd-rx", "config"
	// (LED.Locator), "push" or "reconnect".
	Via      string `json:"via,omitempty"`
	Revision uint64 `json:"revision,omitempty"`
}

// CommandSupport says how an AP command could be sent (AP detail).
type CommandSupport struct {
	// Reboot, RestartRadios and Disconnect are true when the AP's
	// ap-cmd-rx feature is on for this AP (§5.6, experimental).
	Reboot        bool `json:"reboot"`
	RestartRadios bool `json:"restart_radios"`
	Disconnect    bool `json:"disconnect"`
	// Locate is true when LED.Locator is offered (the site is experimental).
	Locate bool `json:"locate"`
	// Fallback is the LAN-side command for an unsupported action.
	Fallback string `json:"fallback"`
}
