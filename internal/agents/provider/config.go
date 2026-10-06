package provider

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	pkgentity "github.com/yogasw/wick/pkg/entity"
)

// InstanceConfig is the wick-tag-annotated view of provider instance settings.
// Used to drive ConfigsTable rendering and per-key saves.
type InstanceConfig struct {
	Binary        string `wick:"key=binary;desc=Binary path override. Empty = auto-resolve from PATH."`
	ExtraArgs     string `wick:"key=extra_args;kvlist;desc=Extra CLI args passed to the binary on every spawn."`
	Env           string `wick:"key=env;kvlist=key|value;desc=Environment variables injected on every spawn."`
	MaxConcurrent int    `wick:"key=max_concurrent;desc=Max parallel spawns (0 = unlimited, follows global cap)."`
	// SendMode picks how a user message reaches the CLI. "default" follows
	// the provider type (claude → append, codex → queue). append = one
	// persistent process, CLI queues input itself. queue = one-shot per
	// turn, mid-turn messages wait then run in order (none lost). spawn =
	// one-shot, every message its own parallel process (no queue, contexts
	// independent — only safe where turns don't need shared history).
	SendMode string `wick:"key=send_mode;dropdown=default|append|queue|spawn;desc=How a message reaches the CLI.\ndefault — follow the provider type (claude=append, codex, omp, opencode=queue).\nappend — one persistent process, the CLI queues input itself (claude). Not supported by codex, omp or opencode: they read the prompt once, so append runs as queue there.\nqueue — one process per turn, messages sent while busy wait and then run TOGETHER as one turn. Context continues (resume). Nothing is dropped. omp and opencode in server mode take them into the running turn instead (omp: steer), so append is supported there.\nspawn — one process per message, all in parallel. No queue, each runs in its own session, so contexts do NOT share history."`
}

// CLIModelConfig is the model-picker section for CLI providers
// (claude/codex/gemini). Kept separate from InstanceConfig so it's only
// appended for those types — wick has its own WickModels UI.
type CLIModelConfig struct {
	ModelSelect bool   `wick:"bool;key=model_select;group=Model selection|Let sessions pick a model for this instance. When on, the composer shows a picker of the models below and passes the choice to the CLI via --model.;desc=Off = the CLI's own default model."`
	Models      string `wick:"key=models;kvlist=id|desc;group=Model selection;visible_when=model_select:true;desc=Models the picker offers (id/alias + short description). Empty = this provider's built-in defaults."`
}

// AccountCLIConfig is the omp/opencode-only section: extra MCP servers.
type AccountCLIConfig struct {
	ExtraMCPServers string `wick:"key=extra_mcp_servers;textarea;desc=Extra MCP servers for this instance, JSON in mcpServers shape — e.g. {\"github\": {\"type\": \"http\", \"url\": \"https://…\", \"headers\": {\"Authorization\": \"Bearer ${GITHUB_TOKEN}\"}}}. Merged next to wick's own server (the name \"wick\" is reserved). Secrets must be ${VAR} references to the Env above, never plaintext. MCP from the host (~/.claude.json, ~/.cursor, project opencode.json, …) is NOT loaded — only wick + these."`
}

// LiveCLIModelConfig is the omp/opencode "live from CLI" model list: the
// picker offers what the CLI itself lists, narrowed by a filter. Rendered by
// the FE inside the Model selection card, not as generic rows.
type LiveCLIModelConfig struct {
	LiveModels       bool   `wick:"bool;key=live_models;group=Model selection;desc=Offer the models this instance's CLI lists (refreshed every ~10 min) instead of the manual list."`
	LiveModelFilter  string `wick:"key=live_model_filter;group=Model selection;desc=Filter over the CLI's models: space-separated terms all must match, a|b = either, !term or -term excludes. e.g. claude|gpt !mini. Empty = all."`
	LiveModelDefault string `wick:"key=live_model_default;group=Model selection;desc=Default model among the filtered list. Empty or no longer listed = the first match."`
}

// OpencodeModelConfig is the opencode-only model/hosting section.
type OpencodeModelConfig struct {
	Model       string `wick:"key=opencode_model;desc=provider/model this instance runs (sent as --model), e.g. openai/gpt-5.5. Required: without it opencode silently uses its hosted default model."`
	AllowHosted bool `wick:"bool;key=opencode_allow_hosted;desc=Allow opencode/… hosted models (opencode Zen). On by default so every model the CLI lists is offered, they send the whole conversation to opencode's servers — turn off to keep to your own providers."`
}

// ServerModeConfig is the shared-CLI-server section (opencode today, omp
// next). Generic keys so one FE toggle serves every provider that has it.
type ServerModeConfig struct {
	ServerMode        bool `wick:"bool;key=server_mode;desc=Keep the CLI running between turns instead of one process per turn. opencode: one shared server per instance (~2 s per turn, ~500 MB shared by all sessions). omp: one RPC process per session (no boot per turn, messages sent mid-turn steer the running turn). Off = one process per turn (the old path: ~6 s and up to ~800 MB each, messages sent mid-turn queue and join the next turn). A change applies from the next turn, a server no longer needed stops once no turn is running."`
	ServerIdleMinutes int  `wick:"key=server_idle_minutes;desc=Minutes the server may sit without a turn before it is killed (started again on the next turn). Empty or 0 = the pool idle timeout (Settings → General, default 2 minutes, same as claude/codex), it cannot be turned off."`
}

// ModelRetryConfig is the omp/opencode "refused model" fallback switch.
type ModelRetryConfig struct {
	AutoRetryModel bool `wick:"bool;key=auto_retry_model;desc=Auto-retry with the next model on access error: when the account is refused the model (model_not_found / no access) before the agent answered, run the same message again on the model that last worked, else the next usable one in the live list — at most 2 retries, with a note in the chat. Off = the turn fails and you pick another model."`
}

// SupportsAutoRetryModel reports whether t has the refused-model retry.
func SupportsAutoRetryModel(t Type) bool { return t == TypeOpencode || t == TypeOMP }

// AuthShareConfig is the omp/opencode shared-login section. The FE renders
// it as a dropdown of the instances this one may take its login from.
type AuthShareConfig struct {
	AuthFrom string `wick:"key=auth_from;desc=Use the login of another instance of the same type instead of logging in here. This instance keeps its own profile, config, soul and sessions, only the credentials are shared (omp: wick runs an auth broker for the owner, opencode: auth.json is linked to the owner's). Empty = its own login. The owner cannot itself use another's login."`
}

// ExternalSkillsConfig is opencode's host-skill switch (omp has none, so
// it is not offered there rather than silently ignored).
type ExternalSkillsConfig struct {
	LoadExternalSkills bool `wick:"bool;key=load_external_skills;desc=Load Claude/Codex skills: let the CLI scan the host's ~/.claude/skills and ~/.agents skill dirs. Off = only the instance's own skills."`
}

// SupportsServerMode reports whether t has the shared-server mode.
func SupportsServerMode(t Type) bool { return t == TypeOpencode || t == TypeOMP }

// SeedInstanceConfig returns populated entity.Config rows for an Instance.
func SeedInstanceConfig(ins Instance) []pkgentity.Config {
	sendMode := ins.SendMode
	if sendMode == "" {
		sendMode = "default" // empty = follow the provider type's default
	}
	rows := pkgentity.StructToConfigs(InstanceConfig{
		Binary:        ins.Binary,
		ExtraArgs:     argsToKVList(ins.ExtraArgs),
		Env:           envToKVList(ins.Env),
		MaxConcurrent: ins.MaxConcurrent,
		SendMode:      sendMode,
	})
	if ins.Type == TypeOMP || ins.Type == TypeOpencode {
		rows = append(rows, pkgentity.StructToConfigs(AccountCLIConfig{ExtraMCPServers: ins.ExtraMCPServers})...)
		rows = append(rows, pkgentity.StructToConfigs(LiveCLIModelConfig{
			LiveModels:       ins.LiveModels,
			LiveModelFilter:  ins.LiveModelFilter,
			LiveModelDefault: ins.LiveModelDefault,
		})...)
	}
	if ins.Type == TypeOpencode {
		oc := OpencodeModelConfig{AllowHosted: true}
		if ins.OpencodeConfig != nil {
			oc.Model = ins.OpencodeConfig.Model
			oc.AllowHosted = ins.OpencodeConfig.AllowHosted
		}
		rows = append(rows, pkgentity.StructToConfigs(oc)...)
	}
	if SupportsServerMode(ins.Type) {
		rows = append(rows, pkgentity.StructToConfigs(ServerModeConfig{
			ServerMode:        !ins.RunPerTurn,
			ServerIdleMinutes: ins.ServerIdleMinutes,
		})...)
	}
	if SupportsAutoRetryModel(ins.Type) {
		rows = append(rows, pkgentity.StructToConfigs(ModelRetryConfig{AutoRetryModel: ins.AutoRetryModel})...)
	}
	if SharesAuth(ins.Type) {
		share := pkgentity.StructToConfigs(AuthShareConfig{AuthFrom: ins.AuthFrom})
		// A dropdown of the instances it may take the login from ("" =
		// its own login; the FE adds that choice).
		if all, err := loadInstances(); err == nil {
			for i := range share {
				share[i].Type = "dropdown"
				share[i].Options = strings.Join(AuthOwnerChoices(all, ins), "|")
			}
		}
		rows = append(rows, share...)
	}
	if ins.Type == TypeOpencode {
		rows = append(rows, pkgentity.StructToConfigs(ExternalSkillsConfig{LoadExternalSkills: ins.LoadExternalSkills})...)
	}
	if CanCompact(ins.Type) {
		trigger := ins.IdleCompactTrigger
		if trigger == "" {
			trigger = IdleCompactPercent
		}
		rows = append(rows, pkgentity.StructToConfigs(IdleCompactConfig{
			IdleCompact:          ins.IdleCompact,
			IdleCompactMinutes:   ins.IdleCompactMinutes,
			IdleCompactTrigger:   trigger,
			IdleCompactThreshold: ins.IdleCompactThreshold,
			IdleCompactScope:     ins.IdleCompactScope,
			IdleCompactMatch:     ins.IdleCompactMatch,
		})...)
	}
	// CLI model picker — claude/codex/gemini only (wick uses WickModels).
	if ins.Type != TypeWick {
		rows = append(rows, pkgentity.StructToConfigs(CLIModelConfig{
			ModelSelect: ins.ModelSelect,
			Models:      modelsToKVList(ins.Models),
		})...)
	}
	return rows
}

// ApplyInstanceConfigKey merges one saved key=value into an Instance.
func ApplyInstanceConfigKey(ins *Instance, key, value string) {
	switch key {
	case "binary":
		ins.Binary = strings.TrimSpace(value)
	case "extra_args":
		ins.ExtraArgs = kvListToArgs(value)
	case "env":
		ins.Env = kvListToEnv(value)
	case "max_concurrent":
		n, _ := strconv.Atoi(strings.TrimSpace(value))
		ins.MaxConcurrent = n
	case "send_mode":
		// "default" (or empty) means follow the provider-type default —
		// store as empty so ParseSendMode falls through to the type rule.
		v := strings.TrimSpace(strings.ToLower(value))
		if v == "default" {
			v = ""
		}
		ins.SendMode = v
	case "disabled":
		ins.Disabled = value == "true" || value == "on"
	case "model_select":
		ins.ModelSelect = value == "true" || value == "on"
	case "models":
		ins.Models = kvListToModels(value)
	case "extra_mcp_servers":
		ins.ExtraMCPServers = strings.TrimSpace(value)
	case "live_models":
		ins.LiveModels = value == "true" || value == "on"
	case "live_model_filter":
		ins.LiveModelFilter = strings.TrimSpace(value)
	case "live_model_default":
		ins.LiveModelDefault = strings.TrimSpace(value)
	case "opencode_model":
		ensureOpencodeConfig(ins).Model = strings.TrimSpace(value)
	case "opencode_allow_hosted":
		ensureOpencodeConfig(ins).AllowHosted = value == "true" || value == "on"
	case "server_idle_minutes":
		n, _ := strconv.Atoi(strings.TrimSpace(value))
		ins.ServerIdleMinutes = n
	case "server_mode":
		ins.RunPerTurn = !(value == "true" || value == "on")
	case "load_external_skills":
		ins.LoadExternalSkills = value == "true" || value == "on"
	case "auto_retry_model":
		ins.AutoRetryModel = value == "true" || value == "on"
	case "auth_from":
		ins.AuthFrom = strings.TrimSpace(value)
	case "idle_compact":
		ins.IdleCompact = value == "true" || value == "on"
	case "idle_compact_minutes":
		n, _ := strconv.Atoi(strings.TrimSpace(value))
		ins.IdleCompactMinutes = n
	case "idle_compact_trigger":
		ins.IdleCompactTrigger = strings.TrimSpace(value)
	case "idle_compact_threshold":
		n, _ := strconv.Atoi(strings.TrimSpace(value))
		ins.IdleCompactThreshold = n
	case "idle_compact_scope":
		ins.IdleCompactScope = value
	case "idle_compact_match":
		ins.IdleCompactMatch = value
	}
}

func ensureOpencodeConfig(ins *Instance) *OpencodeConfig {
	if ins.OpencodeConfig == nil {
		ins.OpencodeConfig = &OpencodeConfig{}
	}
	return ins.OpencodeConfig
}

// ValidateInstanceConfigKey rejects a value before it is saved; "" = fine.
func ValidateInstanceConfigKey(key, value string) error {
	if strings.HasPrefix(key, "idle_compact") {
		return validateIdleCompactKey(key, value)
	}
	switch key {
	case "extra_mcp_servers":
		_, err := ParseExtraMCP(value)
		return err
	case "opencode_model":
		v := strings.TrimSpace(value)
		if v != "" && !strings.Contains(v, "/") {
			return fmt.Errorf("opencode model must be provider/model, got %q", v)
		}
	case "server_idle_minutes":
		if v := strings.TrimSpace(value); v != "" {
			if n, err := strconv.Atoi(v); err != nil || n < 0 {
				return fmt.Errorf("opencode server idle minutes must be a whole number of minutes, got %q", v)
			}
		}
	}
	return nil
}

// modelsToKVList encodes []ModelEntry → JSON [{"id":"opus","desc":"…"}, ...]
// for the id|desc kvlist widget. Empty → "" so the widget renders no rows.
func modelsToKVList(models []ModelEntry) string {
	if len(models) == 0 {
		return ""
	}
	b, err := json.Marshal(models)
	if err != nil {
		return ""
	}
	return string(b)
}

// kvListToModels decodes the id|desc kvlist JSON [{"id":..,"desc":..}, ...]
// back to []ModelEntry, dropping rows with a blank id. Tolerant of the legacy
// single-column {"value":..} shape so a config saved before the desc column
// still loads (value → id).
func kvListToModels(s string) []ModelEntry {
	var rows []map[string]string
	if err := json.Unmarshal([]byte(s), &rows); err != nil || len(rows) == 0 {
		return nil
	}
	out := make([]ModelEntry, 0, len(rows))
	for _, r := range rows {
		id := strings.TrimSpace(r["id"])
		if id == "" {
			id = strings.TrimSpace(r["value"]) // legacy single-column shape
		}
		if id == "" {
			continue
		}
		out = append(out, ModelEntry{ID: id, Desc: strings.TrimSpace(r["desc"])})
	}
	return out
}

// argsToKVList encodes []string → JSON [{"value":"arg"}, ...]
func argsToKVList(args []string) string {
	if len(args) == 0 {
		return ""
	}
	b := strings.Builder{}
	b.WriteString("[")
	for i, a := range args {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"value":`)
		b.WriteString(jsonString(a))
		b.WriteString("}")
	}
	b.WriteString("]")
	return b.String()
}

// kvListToArgs decodes JSON [{"value":"arg"}, ...] → []string
func kvListToArgs(s string) []string {
	var rows []map[string]string
	if err := json.Unmarshal([]byte(s), &rows); err != nil || len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if v := r["value"]; v != "" {
			out = append(out, v)
		}
	}
	return out
}

// envToKVList encodes []string KEY=VALUE → JSON [{"key":"K","value":"V"}, ...]
func envToKVList(env []string) string {
	if len(env) == 0 {
		return ""
	}
	b := strings.Builder{}
	b.WriteString("[")
	for i, e := range env {
		k, v := e, ""
		if idx := strings.IndexByte(e, '='); idx >= 0 {
			k, v = e[:idx], e[idx+1:]
		}
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"key":`)
		b.WriteString(jsonString(k))
		b.WriteString(`,"value":`)
		b.WriteString(jsonString(v))
		b.WriteString("}")
	}
	b.WriteString("]")
	return b.String()
}

// kvListToEnv decodes JSON [{"key":"K","value":"V"}, ...] → []string KEY=VALUE
func kvListToEnv(s string) []string {
	var rows []map[string]string
	if err := json.Unmarshal([]byte(s), &rows); err != nil || len(rows) == 0 {
		return nil
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		if k := r["key"]; k != "" {
			out = append(out, k+"="+r["value"])
		}
	}
	return out
}
