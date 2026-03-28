package workflow

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
