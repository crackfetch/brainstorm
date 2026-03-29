package workflow

import "time"

// Workflow defines a complete browser automation workflow loaded from YAML.
type Workflow struct {
	Name    string            `yaml:"name"`
	Env     map[string]string `yaml:"env,omitempty"`
	Actions map[string]Action `yaml:"actions"`
}

// Action is a named sequence of steps with an optional starting URL.
type Action struct {
	URL    string `yaml:"url,omitempty"`
	Headed bool   `yaml:"headed,omitempty"` // show browser window (used by BRZ_HEADED=auto)
	Steps  []Step `yaml:"steps"`
}

// Step is a single browser operation. Exactly one field should be set.
type Step struct {
	// Navigation
	Navigate string `yaml:"navigate,omitempty"`

	// Interactions
	Click    *ClickStep    `yaml:"click,omitempty"`
	Fill     *FillStep     `yaml:"fill,omitempty"`
	Upload   *UploadStep   `yaml:"upload,omitempty"`
	Download *DownloadStep `yaml:"download,omitempty"`

	// Waits
	WaitVisible *WaitStep    `yaml:"wait_visible,omitempty"`
	WaitText    *WaitStep    `yaml:"wait_text,omitempty"`
	WaitURL     *WaitURLStep `yaml:"wait_url,omitempty"`

	// Utilities
	Screenshot string     `yaml:"screenshot,omitempty"`
	Sleep      *SleepStep `yaml:"sleep,omitempty"`
	Eval       string     `yaml:"eval,omitempty"`

	// Control flow
	Label string `yaml:"label,omitempty"` // human-readable step description for logging
}

type ClickStep struct {
	Selector string `yaml:"selector"`
	Text     string `yaml:"text,omitempty"` // match by visible text
	Nth      int    `yaml:"nth,omitempty"`  // 0-indexed, use when multiple matches
	Timeout  string `yaml:"timeout,omitempty"`
}

type FillStep struct {
	Selector string `yaml:"selector"`
	Value    string `yaml:"value"`          // supports ${ENV_VAR} interpolation
	Clear    bool   `yaml:"clear,omitempty"` // clear field before filling
}

type UploadStep struct {
	Selector string `yaml:"selector"`
	Source   string `yaml:"source"` // "result" to use previous step's output, or a path
}

type DownloadStep struct {
	Timeout  string `yaml:"timeout,omitempty"`
	SaveAs   string `yaml:"save_as,omitempty"` // optional filename pattern
}

type WaitStep struct {
	Selector string `yaml:"selector,omitempty"`
	Text     string `yaml:"text,omitempty"`
	State    string `yaml:"state,omitempty"` // visible, attached, hidden
	Timeout  string `yaml:"timeout,omitempty"`
}

type WaitURLStep struct {
	Match   string `yaml:"match"`
	Timeout string `yaml:"timeout,omitempty"`
}

type SleepStep struct {
	Duration string `yaml:"duration"`
}

// ParseTimeout converts a timeout string like "30s" or "2m" to a Duration.
// Defaults to 30s if empty or unparseable.
func ParseTimeout(s string) time.Duration {
	if s == "" {
		return 30 * time.Second
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 30 * time.Second
	}
	return d
}
