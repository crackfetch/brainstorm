package workflow

import "strings"

// InspectResult is the structured output of inspecting a page's interactive elements.
type InspectResult struct {
	OK         bool          `json:"ok"`
	URL        string        `json:"url"`
	Title      string        `json:"title"`
	Elements   []ElementInfo `json:"elements"`
	Total      int           `json:"total"`
	Truncated  bool          `json:"truncated,omitempty"`
	DurationMs int64         `json:"duration_ms"`
	Error      string        `json:"error,omitempty"`
	Screenshot string        `json:"screenshot,omitempty"`
	EvalResult interface{}   `json:"eval_result,omitempty"`
}

// ElementInfo describes a single interactive DOM element.
type ElementInfo struct {
	Selector    string `json:"selector"`
	Tag         string `json:"tag"`
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Text        string `json:"text,omitempty"`
	Href        string `json:"href,omitempty"`
	Value       string `json:"value,omitempty"`
	Role        string `json:"role,omitempty"`
	Hidden      bool   `json:"hidden,omitempty"`
}

// FilterByTag returns elements whose Tag matches any of the given tags.
// Returns all elements if tags is empty.
func FilterByTag(elements []ElementInfo, tags []string) []ElementInfo {
	if len(tags) == 0 {
		return elements
	}
	set := make(map[string]bool, len(tags))
	for _, t := range tags {
		set[strings.ToLower(t)] = true
	}
	var result []ElementInfo
	for _, el := range elements {
		if set[el.Tag] {
			result = append(result, el)
		}
	}
	return result
}

// FilterByName returns elements whose Name matches any of the given names.
// Returns all elements if names is empty.
func FilterByName(elements []ElementInfo, names []string) []ElementInfo {
	if len(names) == 0 {
		return elements
	}
	set := make(map[string]bool, len(names))
	for _, n := range names {
		set[n] = true
	}
	var result []ElementInfo
	for _, el := range elements {
		if set[el.Name] {
			result = append(result, el)
		}
	}
	return result
}

// CompactElement returns a copy of el with only the core fields for token-efficient output.
// Keeps: Selector, Tag, Type, Name, Text. Strips everything else.
func CompactElement(el ElementInfo) ElementInfo {
	return ElementInfo{
		Selector: el.Selector,
		Tag:      el.Tag,
		Type:     el.Type,
		Name:     el.Name,
		Text:     el.Text,
	}
}

// CompactElements applies CompactElement to each element in the slice.
func CompactElements(elements []ElementInfo) []ElementInfo {
	result := make([]ElementInfo, len(elements))
	for i, el := range elements {
		result[i] = CompactElement(el)
	}
	return result
}

// ExtractTagFromSelector parses a CSS selector and returns the tag name
// from the last segment (after CSS combinators like >, +, ~, space).
// Examples: "button.submit" -> "button", "div > button.submit" -> "button",
// "input#email" -> "input", "#id" -> "".
func ExtractTagFromSelector(selector string) string {
	if selector == "" {
		return ""
	}
	// Take the last segment after CSS combinators (space, >, +, ~).
	// "div > button.submit" -> "button.submit"
	seg := selector
	for _, sep := range []string{" > ", " + ", " ~ ", " "} {
		if idx := strings.LastIndex(seg, sep); idx >= 0 {
			seg = strings.TrimSpace(seg[idx+len(sep):])
		}
	}
	// Find where the tag ends (at first #, ., [, or :)
	end := len(seg)
	for i, c := range seg {
		if c == '#' || c == '.' || c == '[' || c == ':' {
			end = i
			break
		}
	}
	return strings.ToLower(seg[:end])
}

// StepSelector returns the CSS selector from a step, or "" if the step
// doesn't target a selector (e.g., navigate, sleep, download).
func StepSelector(step Step) string {
	switch {
	case step.Click != nil:
		return step.Click.Selector
	case step.Fill != nil:
		return step.Fill.Selector
	case step.Select != nil:
		return step.Select.Selector
	case step.WaitVisible != nil:
		return step.WaitVisible.Selector
	default:
		return ""
	}
}

// SimilarElementsJS is JavaScript that finds up to 5 elements of a given tag type.
// Called on step failure to provide context about what elements ARE on the page.
// Takes a tag name parameter and returns a JSON array of element descriptors.
const SimilarElementsJS = `(tag) => {
	const maxResults = 5;
	const elements = tag ? document.querySelectorAll(tag) : document.querySelectorAll('button, input, a, select');
	const results = [];

	for (let i = 0; i < elements.length && results.length < maxResults; i++) {
		const el = elements[i];
		const rect = el.getBoundingClientRect();
		if (rect.width === 0 && rect.height === 0) continue;

		const info = {};
		// Build selector
		if (el.id) {
			info.selector = '#' + CSS.escape(el.id);
		} else if (el.name) {
			info.selector = el.tagName.toLowerCase() + '[name="' + el.name + '"]';
		} else if (el.className && typeof el.className === 'string') {
			const cls = el.className.trim().split(/\s+/).slice(0, 2).map(c => '.' + CSS.escape(c)).join('');
			info.selector = el.tagName.toLowerCase() + cls;
		} else {
			info.selector = el.tagName.toLowerCase();
		}

		info.tag = el.tagName.toLowerCase();
		if (el.type) info.type = el.type;
		if (el.name) info.name = el.name;
		const text = (el.textContent || '').trim().substring(0, 80);
		if (text && ['button', 'a'].includes(info.tag)) info.text = text;

		results.push(info);
	}
	return results;
}`

// inspectJS extracts interactive elements from the DOM.
// Returns a JSON array of element descriptors.
// Designed to be compact and useful for LLM agents writing workflow YAML.
// InspectJS is the JavaScript that extracts interactive elements from the DOM.
// Exported so the CLI can call it via page.Eval().
const InspectJS = `() => {
	const maxFull = 500;
	const full = window.__brz_full || false;

	// Selectors for actionable elements (default mode)
	const actionableSelector = [
		'input', 'textarea', 'select', 'button',
		'a[href]', '[role="button"]', '[role="link"]',
		'[type="file"]', '[contenteditable="true"]'
	].join(', ');

	const candidates = full
		? document.querySelectorAll('*')
		: document.querySelectorAll(actionableSelector);

	function bestSelector(el) {
		if (el.id) return '#' + CSS.escape(el.id);
		if (el.name) return el.tagName.toLowerCase() + '[name="' + el.name + '"]';
		if (el.type && el.type !== 'text')
			return el.tagName.toLowerCase() + '[type="' + el.type + '"]';
		if (el.className && typeof el.className === 'string') {
			const cls = el.className.trim().split(/\s+/).slice(0, 2).map(c => '.' + CSS.escape(c)).join('');
			if (cls && document.querySelectorAll(el.tagName.toLowerCase() + cls).length === 1)
				return el.tagName.toLowerCase() + cls;
		}
		if (el.getAttribute('aria-label'))
			return el.tagName.toLowerCase() + '[aria-label="' + el.getAttribute('aria-label') + '"]';
		// Fallback: tag + nth-of-type
		const parent = el.parentElement;
		if (parent) {
			const siblings = Array.from(parent.children).filter(c => c.tagName === el.tagName);
			if (siblings.length > 1) {
				const idx = siblings.indexOf(el) + 1;
				return el.tagName.toLowerCase() + ':nth-of-type(' + idx + ')';
			}
		}
		return el.tagName.toLowerCase();
	}

	const results = [];
	const seen = new Set();

	for (const el of candidates) {
		if (results.length >= maxFull && full) break;

		const sel = bestSelector(el);
		if (seen.has(sel)) continue;
		seen.add(sel);

		const tag = el.tagName.toLowerCase();
		const rect = el.getBoundingClientRect();
		const isHidden = rect.width === 0 && rect.height === 0;
		const style = window.getComputedStyle(el);
		const isDisplayNone = style.display === 'none' || style.visibility === 'hidden';
		const hidden = isHidden || isDisplayNone;

		// In default mode, include hidden elements (they reveal form structure)
		// In full mode, skip non-interactive hidden elements
		if (full && hidden && !['input', 'textarea', 'select'].includes(tag)) continue;

		const info = { selector: sel, tag: tag };
		if (el.type) info.type = el.type;
		if (el.name) info.name = el.name;
		if (el.placeholder) info.placeholder = el.placeholder;
		if (el.value && el.type === 'hidden') info.value = el.value;
		if (el.href) info.href = el.href;
		if (el.getAttribute('role')) info.role = el.getAttribute('role');
		if (hidden) info.hidden = true;

		// Text content (truncated for buttons/links, skip for inputs)
		if (['button', 'a', 'label'].includes(tag) || info.role === 'button') {
			const text = (el.textContent || '').trim().substring(0, 80);
			if (text) info.text = text;
		}

		results.push(info);
	}

	return {
		title: document.title,
		url: window.location.href,
		total: results.length,
		truncated: full && candidates.length > maxFull,
		elements: results
	};
}`
