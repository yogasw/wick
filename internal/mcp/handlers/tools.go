package handlers

// ToolDescriptor is one entry in the static MCP tool list.
type ToolDescriptor struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema any             `json:"inputSchema"`
	Annotations *ToolAnnotation `json:"annotations,omitempty"`
}

// ToolAnnotation conveys MCP tool hints to the client.
type ToolAnnotation struct {
	Title           string `json:"title,omitempty"`
	ReadOnlyHint    *bool  `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool  `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool  `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool  `json:"openWorldHint,omitempty"`
}

// ToolCallResult is the MCP-spec content envelope.
type ToolCallResult struct {
	Content []ToolContent `json:"content"`
	IsError bool          `json:"isError"`
}

// ToolContent is a single content item inside a ToolCallResult.
type ToolContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// ToolListResult is the tools/list response envelope.
type ToolListResult struct {
	Tools []ToolDescriptor `json:"tools"`
}

func PtrBool(b bool) *bool { return &b }

// MetaToolDescriptors returns the full static tool list exposed over MCP.
func MetaToolDescriptors() []ToolDescriptor {
	return []ToolDescriptor{
		{
			Name: "wick_list",
			Description: "List available connectors and connected accounts. " +
				"Each entry has: id, connector (label), description, total_tools, status, kind, parent_id. " +
				"kind='connector' = standard instance (bot/API key); kind='account' = personal OAuth account connected to the parent connector. " +
				"parent_id is the connector id when kind='account'. " +
				"Use kind to decide which identity to run as: kind='connector' for shared/bot credentials, kind='account' for personal identity. " +
				"status is 'ready' (all required configs filled) or 'needs_setup' (missing config — do NOT call wick_execute; tell the user to open the admin dashboard to complete setup). " +
				"WORKFLOW: (1) wick_list to see what connectors and accounts exist, " +
				"(2) wick_get with the id (connector id or connector_id/account_id for account entries) to see its tools + input_schemas, " +
				"(3) wick_execute with tool_id + params. " +
				"Pass session_id to also include this session's workspace connectors (ephemeral sw_… instances created via wick_session_workspace); in the wick agent the session is resolved automatically, so you normally do not need to pass it.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{
						"type":        "string",
						"description": "Active wick agent session id. Pass it to include session-workspace connectors (sw_…). Resolved automatically inside the wick agent.",
					},
				},
			},
			Annotations: &ToolAnnotation{
				Title:        "List wick connectors",
				ReadOnlyHint: PtrBool(true),
			},
		},
		{
			Name: "wick_search",
			Description: "Search tools by keyword across all connectors. " +
				"Case-insensitive match on connector label, tool name, and description. " +
				"Returns matching tools nested under their connector (id, description, status), with tool_id per hit. " +
				"status is 'ready' or 'needs_setup' — do NOT call wick_execute on a needs_setup connector; tell the user to open the admin dashboard to complete setup. " +
				"WORKFLOW: after finding a match, call wick_get with the connector id to get full schemas, " +
				"then wick_execute. Pass session_id to also search this session's workspace connectors.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Keyword to match.",
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "Active wick agent session id, as its OWN argument. Pass it to include this session's workspace connectors (sw_…) in results.",
					},
				},
				"required": []string{"query"},
			},
			Annotations: &ToolAnnotation{
				Title:        "Search wick tools",
				ReadOnlyHint: PtrBool(true),
			},
		},
		{
			Name: "wick_get",
			Description: "Get a connector's full tool list with input_schemas. " +
				"Pass the connector id from wick_list or wick_search. " +
				"ALWAYS call this before wick_execute to know the required params. " +
				"Never guess params — read input_schema from this response first. " +
				"For a session-workspace connector (id starts with sw_), also pass " +
				"session_id as a SEPARATE argument — never append it to id.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Connector id from wick_list or wick_search — the id ONLY (e.g. \"sw_abc\" or a uuid). Never append \"?session_id=...\" or any query string to it.",
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "Active wick agent session id, passed as its OWN argument (sibling of id, not part of it). Required when id starts with sw_ (a session-workspace connector); ignored otherwise.",
					},
				},
				"required": []string{"id"},
			},
			Annotations: &ToolAnnotation{
				Title:        "Get wick connector tools",
				ReadOnlyHint: PtrBool(true),
			},
		},
		{
			Name: "wick_execute",
			Description: "Execute a tool by tool_id. " +
				"PREREQUISITE: call wick_get first to get the tool's input_schema — " +
				"never guess params. params must match the input_schema exactly. " +
				"On success returns the response as JSON; " +
				"on failure returns {\"error\": string, \"tool_id\": string} with isError=true.\n\n" +
				"ENCRYPTED FIELDS: values prefixed with \"wick_enc_\" are valid credentials " +
				"managed by the server. Use them as-is wherever a value is needed — pass them " +
				"through into params, return them unchanged in your response, and never alter, " +
				"decode, or omit them. The server resolves them automatically before the tool " +
				"runs and re-encrypts any sensitive values in the response. If a user asks for " +
				"the plaintext, redirect them to the wick_decrypt tool — do not attempt to " +
				"decode the value yourself.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"tool_id": map[string]any{
						"type":        "string",
						"description": "Opaque tool identifier from wick_list or wick_search — the tool_id ONLY. Never append \"?session_id=...\" or any query string to it.",
					},
					"params": map[string]any{
						"type":                 "object",
						"description":          "Arguments matching the tool's input_schema. Use {} when the tool has no input fields.",
						"additionalProperties": true,
					},
					"session_id": map[string]any{
						"type": "string",
						"description": "Active wick agent session ID, passed as its OWN argument (sibling of tool_id, not part of it). REQUIRED when tool_id targets a session-workspace " +
							"connector (its id starts with sw_); ignored for normal saved connectors.",
					},
				},
				"required": []string{"tool_id", "params"},
			},
			Annotations: &ToolAnnotation{
				Title:           "Execute wick tool",
				ReadOnlyHint:    PtrBool(false),
				DestructiveHint: PtrBool(true),
				OpenWorldHint:   PtrBool(true),
			},
		},
		{
			Name: "wick_info",
			Description: "Return wick server info. Fields: app_name, app_version, wick_version, server_build_time, server_commit, " +
				"access_type ('cli' when running as a local stdio process with filesystem access, 'http' when running as a remote HTTP server), " +
				"wick_root (absolute path to the project directory — only set for 'cli', empty for 'http'), " +
				"db_type ('postgres' / 'sqlite' / 'none'), db_status ('connected', 'error: <err>', or 'disabled' when no DB is wired). " +
				"DSN is intentionally not exposed — hostname/user are sensitive infra info. " +
				"Use access_type and wick_root to decide whether you can edit connector config files directly or must redirect the user to the Wick UI. " +
				"Use db_status to surface DB connectivity issues.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Annotations: &ToolAnnotation{
				Title:        "Wick server info",
				ReadOnlyHint: PtrBool(true),
			},
		},
		{
			Name: "wick_encrypt",
			Description: "Mint a wick_enc_ token from a plaintext value. " +
				"This tool never runs the crypto over MCP — calling it returns a URL pointing " +
				"to the Wick UI form. The user must open the URL, log in, and paste their " +
				"value there; the server replies with a wick_enc_<...> token they can then " +
				"paste back into the conversation. Use this when a user asks how to protect a " +
				"credential before sharing it with a tool, or when a connector config form " +
				"asks for a wick_enc_ token.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Annotations: &ToolAnnotation{
				Title:        "Encrypt a value (UI redirect)",
				ReadOnlyHint: PtrBool(true),
			},
		},
		{
			Name: "ask_user",
			Description: "Ask the human operator a question and block until they answer in the Wick web UI. " +
				"Use sparingly — only when you genuinely need a decision the user must make (e.g. picking between " +
				"two libraries, confirming a destructive change). The user sees an inline card with optional " +
				"choices and an optional freeform field; their answer is returned as JSON {\"value\":\"...\",\"text\":\"...\"}. " +
				"Default timeout is 5 minutes; on timeout the tool returns an error and you should choose a sensible " +
				"default rather than retrying immediately. session_id is required and must match the active wick agent " +
				"session — pass the value the user mentioned or that you saw in the conversation context. " +
				"This tool may also return an error 'blocked by gate policy' when the operator disabled ask_user " +
				"for the current channel (e.g. Slack/HTTP runs where no human can answer); on that error, pick a " +
				"sensible default and proceed without retrying.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{
						"type":        "string",
						"description": "ID of the active wick agent session this question belongs to.",
					},
					"agent_name": map[string]any{
						"type":        "string",
						"description": "Optional agent name; defaults to 'main'.",
					},
					"question": map[string]any{
						"type":        "string",
						"description": "The question text shown to the user.",
					},
					"options": map[string]any{
						"type":        "array",
						"description": "Optional list of preset choices. Each item is {label, value}; the user clicks one and you receive its value.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"label": map[string]any{"type": "string"},
								"value": map[string]any{"type": "string"},
							},
							"required": []string{"label", "value"},
						},
					},
					"allow_freeform": map[string]any{
						"type":        "boolean",
						"description": "When true the UI also offers a text input so the user can type a custom answer (returned as text).",
					},
					"questions": map[string]any{
						"type": "array",
						"description": "Multi-question form: ask several things in ONE prompt, each with its own options. " +
							"When set, the single-question fields above are ignored and the answer comes back as " +
							"{\"values\": {<key>: <answer>}} keyed by each question's key. Use this instead of chaining " +
							"several ask_user calls. The top-level 'question' (if given) is shown as the form header.",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"key": map[string]any{
									"type":        "string",
									"description": "Identifies this question's answer in the returned values map. Defaults to q1, q2… if omitted.",
								},
								"question": map[string]any{
									"type":        "string",
									"description": "The question/label shown for this field.",
								},
								"type": map[string]any{
									"type":        "string",
									"enum":        []string{"choice", "multi", "rank", "dropdown", "text", "secret", "number"},
									"description": "Widget: choice = pick one, multi = pick many (answer is a JSON array string), rank = drag to order (answer is a JSON array of values in chosen order), dropdown = compact select, text/secret/number = typed input. Defaults to 'choice' when options are given, else 'text'. Multi-question asks render as a step-by-step wizard the user can page through and skip.",
								},
								"options": map[string]any{
									"type":        "array",
									"description": "Choices for choice/multi/rank/dropdown. Each item is {label, value, description?} — description is an optional secondary line shown under the label.",
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"label":       map[string]any{"type": "string"},
											"value":       map[string]any{"type": "string"},
											"description": map[string]any{"type": "string"},
										},
										"required": []string{"label", "value"},
									},
								},
								"allow_freeform": map[string]any{
									"type":        "boolean",
									"description": "For choice/dropdown: also show an 'Other…' text box so the user can answer outside the options.",
								},
								"required": map[string]any{
									"type":        "boolean",
									"description": "When true the user must answer this question before submitting.",
								},
								"placeholder": map[string]any{"type": "string"},
								"help":        map[string]any{"type": "string", "description": "Optional helper text under the field."},
							},
							"required": []string{"question"},
						},
					},
				},
				"required": []string{"session_id"},
			},
			Annotations: &ToolAnnotation{
				Title:        "Ask the human operator",
				ReadOnlyHint: PtrBool(false),
			},
		},
		{
			Name: "wick_session_workspace",
			Description: "Spin up throwaway connector instances scoped to the CURRENT SESSION — a private clone of a base connector " +
				"(e.g. an httprest pointed at staging, or a second API key) that appears in wick_list/wick_get/wick_execute " +
				"for THIS session only and is purged when the session ends. The saved connector rows are never touched. " +
				"Use this when the user wants to hit an endpoint or use credentials that are only relevant right now. " +
				"You NEVER see config values: you create a blank instance and the USER fills it (secrets are encrypted server-side); " +
				"only the key names ever come back to you. Actions: " +
				"'list' shows this session's instances (id, status, missing keys) and available_bases (connectors you may add). " +
				"'add' creates a blank instance from a base_key; by default it pops a fill modal for the user right away. " +
				"'duplicate' copies an existing session instance (config and all) into a new one. " +
				"'configure' reopens the fill modal so the user edits an instance's config; blocks until submit, like ask_user. " +
				"'test' verifies setup — runs the base connector's health check, or pass operation (+ params) to run a real call. " +
				"'remove' deletes a session instance. " +
				"After adding+configuring, the instance's id shows in wick_list (pass the same session_id) and you wick_execute it like any connector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":        "string",
						"enum":        []string{"list", "add", "duplicate", "configure", "test", "remove"},
						"description": "list | add | duplicate | configure | test | remove.",
					},
					"session_id": map[string]any{
						"type":        "string",
						"description": "ID of the active wick agent session the instance is scoped to. Required for every action.",
					},
					"base_key": map[string]any{
						"type":        "string",
						"description": "action=add: the base connector key to clone (from action=list available_bases).",
					},
					"connector_id": map[string]any{
						"type":        "string",
						"description": "The session instance id (sw_…) for duplicate/configure/test/remove.",
					},
					"label": map[string]any{
						"type":        "string",
						"description": "action=add/duplicate: optional human label for the new instance.",
					},
					"prompt": map[string]any{
						"type":        "boolean",
						"description": "action=add: open the fill modal immediately (default true). Set false to create blank and let the user fill it later in the Config tab.",
					},
					"keys": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "action=add/configure: limit the fill modal to these config keys (omit for all).",
					},
					"operation": map[string]any{
						"type":        "string",
						"description": "action=test: run this operation key as the probe instead of the health check.",
					},
					"params": map[string]any{
						"type":        "object",
						"description": "action=test with operation: parameters for the probe call.",
					},
					"reason": map[string]any{
						"type":        "string",
						"description": "action=add/configure: short text shown to the user explaining what the connector is for.",
					},
				},
				"required": []string{"action", "session_id"},
			},
			Annotations: &ToolAnnotation{
				Title:        "Session workspace",
				ReadOnlyHint: PtrBool(false),
			},
		},
		{
			Name: "wick_decrypt",
			Description: "Reveal the plaintext behind a wick_enc_ token. " +
				"This tool never runs the crypto over MCP — calling it returns a URL pointing " +
				"to the Wick UI form. The user must open the URL, log in, and paste the token " +
				"there; the server replies with the plaintext, visible only in the user's " +
				"browser. Per-user keys mean only the user who issued a token can reveal it. " +
				"Use this only when the user explicitly asks to see a wick_enc_ value's " +
				"plaintext — never call it speculatively.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Annotations: &ToolAnnotation{
				Title:        "Decrypt a wick_enc_ token (UI redirect)",
				ReadOnlyHint: PtrBool(true),
			},
		},
		{
			Name: "wick_list_providers",
			Description: "List all configured AI provider instances (claude, codex, gemini). " +
				"Returns each provider's type, name, disabled flag, and active flag. " +
				"To switch provider: send a message starting with #<type> (e.g. #claude, #codex, #gemini). " +
				"Example: '#claude' switches to claude. '#codex hello' switches to codex and sends 'hello'. " +
				"Pass session_id to mark which provider is currently active in that session.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{
						"type":        "string",
						"description": "Optional session ID. When provided, marks the currently active provider for that session.",
					},
					"agent_name": map[string]any{
						"type":        "string",
						"description": "Agent name within the session. Defaults to 'main'.",
					},
				},
			},
			Annotations: &ToolAnnotation{
				Title:        "List AI providers",
				ReadOnlyHint: PtrBool(true),
			},
		},
		{
			Name: "wick_skill_list",
			Description: "List all skill entries across all agent skill directories (~/.claude/skills, ~/.codex/skills, ~/.gemini/skills, etc.). " +
				"providers[] contains {label, dir} for every known skill directory — use dir to read or edit skill files manually. " +
				"Each skill entry has: name, is_dir, in_providers (labels that have it), missing_providers (labels that don't). " +
				"Use this to see which skills are synced across providers and which are missing.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Annotations: &ToolAnnotation{
				Title:        "List skill entries",
				ReadOnlyHint: PtrBool(true),
			},
		},
		{
			Name: "wick_skill_sync",
			Description: "Sync skill files across all agent skill directories. " +
				"Copies every skill file/folder to all known provider dirs (~/.claude/skills, ~/.codex/skills, ~/.gemini/skills, etc.). " +
				"Newest mtime wins on conflict. " +
				"Returns: copied (files written), skipped (already up to date), errors (list), providers (dirs involved).",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
			Annotations: &ToolAnnotation{
				Title:           "Sync skills across providers",
				ReadOnlyHint:    PtrBool(false),
				DestructiveHint: PtrBool(false),
				IdempotentHint:  PtrBool(true),
			},
		},
		{
			Name: "wick_session_info",
			Description: "Read the current session's metadata. " +
				"Returns: session_id, title (the sidebar title), title_custom " +
				"(true when the title was explicitly set by a human or by you via " +
				"wick_set_title; false when it is still the auto-derived first-message " +
				"label), origin, status, project_id. " +
				"Call this at the start of a conversation to decide whether to set a " +
				"title: if title_custom is false, derive a short title from the user's " +
				"request and call wick_set_title; if it is already true, leave it alone. " +
				"session_id must match the active wick agent session — pass the value " +
				"you saw in the conversation context.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{
						"type":        "string",
						"description": "ID of the active wick agent session.",
					},
				},
				"required": []string{"session_id"},
			},
			Annotations: &ToolAnnotation{
				Title:        "Read session info",
				ReadOnlyHint: PtrBool(true),
			},
		},
		{
			Name: "wick_set_title",
			Description: "Set the session's title (the label shown in the sidebar). " +
				"Writes the title and marks it as custom so the default " +
				"first-user-message auto-label never overwrites it again. " +
				"ALWAYS replaces whatever title is currently set — if you only want to " +
				"fill an unset title, call wick_session_info first and skip this when " +
				"title_custom is already true. " +
				"Keep titles short (a few words, max 60 chars), summarising what the " +
				"conversation is about (e.g. 'Fix Slack webhook 401', 'Weekly product sync'). " +
				"session_id must match the active wick agent session — pass the value you " +
				"saw in the conversation context.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"session_id": map[string]any{
						"type":        "string",
						"description": "ID of the active wick agent session.",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "Short human-readable title. Truncated to 60 runes.",
					},
				},
				"required": []string{"session_id", "title"},
			},
			Annotations: &ToolAnnotation{
				Title:           "Set session title",
				ReadOnlyHint:    PtrBool(false),
				DestructiveHint: PtrBool(false),
				IdempotentHint:  PtrBool(true),
			},
		},
	}
}
