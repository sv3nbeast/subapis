package antigravity

import "strings"

type officialAntigravityToolSpec struct {
	Name        string
	Description string
}

var officialAntigravityToolSpecs = map[string]officialAntigravityToolSpec{
	"command_status": {
		Name:        "command_status",
		Description: "Get the status of a previously executed terminal command by its ID. Returns the current status (running, done), output lines as specified by output priority, and any error if present. Do not try to check the status of any IDs other than Background command IDs.",
	},
	"generate_image": {
		Name:        "generate_image",
		Description: "Generate an image or edit existing images based on a text prompt. The resulting image will be saved as an artifact for use. You can use this tool to generate user interfaces and iterate on a design with the USER for an application or website that you are building. When creating UI designs, generate only the interface itself without surrounding device frames unless the user explicitly requests them.",
	},
	"grep_search": {
		Name:        "grep_search",
		Description: "Use ripgrep to find exact pattern matches within files or directories. Always use this tool for repository text and code search instead of shell grep commands unless the user explicitly requests shell command execution.",
	},
	"list_dir": {
		Name:        "list_dir",
		Description: "List the contents of a directory, including files and subdirectories. Prefer this tool over shell ls/find for workspace discovery when a structured directory listing is enough.",
	},
	"multi_replace_file_content": {
		Name:        "multi_replace_file_content",
		Description: "Edit an existing file with multiple non-contiguous replacements. Use this tool only when changing more than one separate block in the same file; for a single contiguous edit, use replace_file_content instead.",
	},
	"read_url_content": {
		Name:        "read_url_content",
		Description: "Fetch content from a URL via HTTP request and convert HTML to markdown. Use this for reading public pages or static documentation without JavaScript execution.",
	},
	"replace_file_content": {
		Name:        "replace_file_content",
		Description: "Edit an existing file with a single contiguous replacement. Use this tool only for one contiguous block of edits in the same file; for multiple non-adjacent edits, use multi_replace_file_content instead.",
	},
	"run_command": {
		Name:        "run_command",
		Description: "Propose a shell command to run on behalf of the user. Do not use this tool for normal repository search, file reading, or deterministic file edits when structured workspace tools can express the task.",
	},
	"search_web": {
		Name:        "search_web",
		Description: "Perform a web search for a given query and return a summary with citations.",
	},
	"send_command_input": {
		Name:        "send_command_input",
		Description: "Send standard input to a running command or terminate a running command. Use this to interact with REPLs, long-running processes, and previously started terminal commands.",
	},
	"view_file": {
		Name:        "view_file",
		Description: "View the contents of a file from the local filesystem. Prefer this tool over shell cat, sed, head, or tail commands for normal file inspection.",
	},
	"write_to_file": {
		Name:        "write_to_file",
		Description: "Create a new file and write complete contents to it. Prefer this tool over shell redirection or heredocs for new-file creation.",
	},
}

func normalizeOfficialAntigravityToolToken(name string) string {
	name = strings.TrimSpace(strings.ToLower(name))
	if name == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(name))
	lastUnderscore := false
	for _, r := range name {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if isAlphaNum {
			_, _ = b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			_ = b.WriteByte('_')
			lastUnderscore = true
		}
	}
	return strings.Trim(b.String(), "_")
}

func officialAntigravityToolSpecForName(name string) (officialAntigravityToolSpec, bool) {
	normalized := normalizeOfficialAntigravityToolToken(name)
	spec, ok := officialAntigravityToolSpecs[normalized]
	return spec, ok
}

func toOfficialAntigravityToolName(name string) string {
	normalized := normalizeOfficialAntigravityToolToken(name)
	nameMap := map[string]string{
		"read_file":                  "view_file",
		"read_file_v2":               "view_file",
		"view_file":                  "view_file",
		"list_directory":             "list_dir",
		"list_dir":                   "list_dir",
		"grep_search":                "grep_search",
		"ripgrep_search":             "grep_search",
		"edit_file":                  "replace_file_content",
		"edit_file_v2":               "replace_file_content",
		"replace_file_content":       "replace_file_content",
		"multi_replace_file_content": "multi_replace_file_content",
		"write_to_file":              "write_to_file",
		"run_terminal_command":       "run_command",
		"run_terminal_command_v2":    "run_command",
		"shell":                      "run_command",
		"run_command":                "run_command",
		"background_shell_spawn":     "run_command",
		"write_shell_stdin":          "send_command_input",
		"send_command_input":         "send_command_input",
		"command_status":             "command_status",
		"web_search":                 "search_web",
		"search_web":                 "search_web",
		"web_fetch":                  "read_url_content",
		"read_url_content":           "read_url_content",
		"generate_image":             "generate_image",
		"browser_subagent":           "browser_subagent",
	}
	if mapped, ok := nameMap[normalized]; ok {
		return mapped
	}
	return normalized
}

func defaultClientAntigravityToolName(officialName string) string {
	normalized := normalizeOfficialAntigravityToolToken(officialName)
	nameMap := map[string]string{
		"view_file":                  "view_file",
		"list_dir":                   "list_dir",
		"grep_search":                "grep_search",
		"replace_file_content":       "replace_file_content",
		"multi_replace_file_content": "multi_replace_file_content",
		"write_to_file":              "write_to_file",
		"run_command":                "run_command",
		"send_command_input":         "send_command_input",
		"command_status":             "command_status",
		"search_web":                 "search_web",
		"read_url_content":           "read_url_content",
		"generate_image":             "generate_image",
		"browser_subagent":           "browser_subagent",
	}
	if mapped, ok := nameMap[normalized]; ok {
		return mapped
	}
	return officialName
}

func buildOfficialAntigravityToolNameMaps(tools []ClaudeTool) (map[string]string, map[string]string) {
	clientToOfficial := make(map[string]string)
	officialToClient := make(map[string]string)
	for _, tool := range tools {
		originalName := strings.TrimSpace(tool.Name)
		if originalName == "" {
			continue
		}
		officialName := toOfficialAntigravityToolName(originalName)
		clientToOfficial[originalName] = officialName
		if _, exists := officialToClient[officialName]; !exists {
			officialToClient[officialName] = originalName
		}
	}
	return clientToOfficial, officialToClient
}

// BuildOfficialAntigravityToolNameMaps returns:
// 1. client/tool-definition name -> official Antigravity tool name
// 2. official Antigravity tool name -> preferred client-facing original tool name
func BuildOfficialAntigravityToolNameMaps(tools []ClaudeTool) (map[string]string, map[string]string) {
	return buildOfficialAntigravityToolNameMaps(tools)
}

func canonicalizeOfficialAntigravityToolInvocation(toolName string, input any) (string, map[string]any, bool) {
	officialName := toOfficialAntigravityToolName(toolName)
	inputMap, ok := input.(map[string]any)
	if !ok {
		return officialName, map[string]any{}, false
	}

	firstString := func(keys ...string) string {
		for _, key := range keys {
			if value, ok := inputMap[key]; ok {
				if s, ok := value.(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
		return ""
	}
	firstBool := func(keys ...string) (bool, bool) {
		for _, key := range keys {
			if value, ok := inputMap[key]; ok {
				if b, ok := value.(bool); ok {
					return b, true
				}
			}
		}
		return false, false
	}
	firstInt := func(keys ...string) (int, bool) {
		for _, key := range keys {
			if value, ok := inputMap[key]; ok {
				switch v := value.(type) {
				case int:
					return v, true
				case float64:
					return int(v), true
				}
			}
		}
		return 0, false
	}
	firstStringSlice := func(keys ...string) []string {
		for _, key := range keys {
			if value, ok := inputMap[key]; ok {
				if items, ok := value.([]any); ok {
					out := make([]string, 0, len(items))
					for _, item := range items {
						if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
							out = append(out, strings.TrimSpace(s))
						}
					}
					if len(out) > 0 {
						return out
					}
				}
			}
		}
		return nil
	}

	switch officialName {
	case "view_file":
		out := map[string]any{}
		if path := firstString("AbsolutePath", "path", "file_path"); path != "" {
			out["AbsolutePath"] = path
		}
		if startLine, ok := firstInt("StartLine", "start_line", "startLine"); ok {
			out["StartLine"] = startLine
		}
		if endLine, ok := firstInt("EndLine", "end_line", "endLine"); ok {
			out["EndLine"] = endLine
		}
		if isSkill, ok := firstBool("IsSkillFile", "is_skill_file", "isSkillFile"); ok {
			out["IsSkillFile"] = isSkill
		}
		return officialName, out, true
	case "list_dir":
		out := map[string]any{}
		if path := firstString("DirectoryPath", "path"); path != "" {
			out["DirectoryPath"] = path
		}
		return officialName, out, true
	case "grep_search":
		out := map[string]any{}
		if path := firstString("SearchPath", "search_path", "searchPath", "path"); path != "" {
			out["SearchPath"] = path
		}
		if query := firstString("Query", "query", "pattern", "searchTerm", "search_term"); query != "" {
			out["Query"] = query
		}
		if includes := firstStringSlice("Includes", "includes", "include", "glob", "globs"); len(includes) > 0 {
			items := make([]any, 0, len(includes))
			for _, item := range includes {
				items = append(items, item)
			}
			out["Includes"] = items
		}
		if isRegex, ok := firstBool("IsRegex", "isRegex", "is_regex"); ok {
			out["IsRegex"] = isRegex
		}
		if caseInsensitive, ok := firstBool("CaseInsensitive", "caseInsensitive", "case_insensitive"); ok {
			out["CaseInsensitive"] = caseInsensitive
		}
		if matchPerLine, ok := firstBool("MatchPerLine", "matchPerLine", "match_per_line"); ok {
			out["MatchPerLine"] = matchPerLine
		}
		return officialName, out, true
	case "run_command":
		out := map[string]any{}
		if cwd := firstString("Cwd", "cwd", "working_directory", "workingDirectory"); cwd != "" {
			out["Cwd"] = cwd
		}
		if command := firstString("CommandLine", "command", "cmd"); command != "" {
			out["CommandLine"] = command
		}
		if waitMS, ok := firstInt("WaitMsBeforeAsync", "waitMsBeforeAsync", "wait_ms_before_async"); ok {
			out["WaitMsBeforeAsync"] = waitMS
		}
		if safe, ok := firstBool("SafeToAutoRun", "safeToAutoRun", "safe_to_auto_run"); ok {
			out["SafeToAutoRun"] = safe
		}
		if runPersistent, ok := firstBool("RunPersistent", "runPersistent", "run_persistent"); ok {
			out["RunPersistent"] = runPersistent
		}
		if terminalID := firstString("RequestedTerminalID", "requestedTerminalId", "requested_terminal_id"); terminalID != "" {
			out["RequestedTerminalID"] = terminalID
		}
		return officialName, out, true
	case "send_command_input":
		out := map[string]any{}
		if commandID := firstString("CommandId", "commandId", "command_id", "shellId", "shell_id"); commandID != "" {
			out["CommandId"] = commandID
		}
		if inputText, ok := inputMap["Input"]; ok {
			out["Input"] = inputText
		} else if data, ok := inputMap["data"]; ok {
			out["Input"] = data
		}
		if terminate, ok := firstBool("Terminate", "terminate", "shouldTerminate"); ok {
			out["Terminate"] = terminate
		}
		if waitMS, ok := firstInt("WaitMs", "waitMs", "wait_ms"); ok {
			out["WaitMs"] = waitMS
		}
		if safe, ok := firstBool("SafeToAutoRun", "safeToAutoRun", "safe_to_auto_run"); ok {
			out["SafeToAutoRun"] = safe
		}
		return officialName, out, true
	case "search_web":
		out := map[string]any{}
		if query := firstString("query", "search_query", "searchQuery"); query != "" {
			out["query"] = query
		}
		if domain := firstString("domain"); domain != "" {
			out["domain"] = domain
		}
		return officialName, out, true
	case "read_url_content":
		out := map[string]any{}
		if targetURL := firstString("Url", "url", "URL"); targetURL != "" {
			out["Url"] = targetURL
		}
		return officialName, out, true
	case "command_status":
		out := map[string]any{}
		if commandID := firstString("CommandId", "commandId", "command_id"); commandID != "" {
			out["CommandId"] = commandID
		}
		if outputChars, ok := firstInt("OutputCharacterCount", "outputCharacterCount", "output_character_count"); ok {
			out["OutputCharacterCount"] = outputChars
		}
		if waitSeconds, ok := firstInt("WaitDurationSeconds", "waitDurationSeconds", "wait_duration_seconds"); ok {
			out["WaitDurationSeconds"] = waitSeconds
		}
		return officialName, out, true
	case "generate_image":
		out := map[string]any{}
		if imageName := firstString("ImageName", "imageName", "image_name", "filePath"); imageName != "" {
			out["ImageName"] = imageName
		}
		if prompt := firstString("Prompt", "prompt"); prompt != "" {
			out["Prompt"] = prompt
		}
		if imagePaths := firstStringSlice("ImagePaths", "imagePaths", "image_paths", "referenceImagePaths"); len(imagePaths) > 0 {
			items := make([]any, 0, len(imagePaths))
			for _, item := range imagePaths {
				items = append(items, item)
			}
			out["ImagePaths"] = items
		}
		return officialName, out, true
	default:
		return officialName, inputMap, false
	}
}

func adaptOfficialAntigravityToolInput(officialName string, input any) any {
	inputMap, ok := input.(map[string]any)
	if !ok {
		return input
	}
	switch normalizeOfficialAntigravityToolToken(officialName) {
	case "view_file":
		out := map[string]any{}
		if value, ok := inputMap["AbsolutePath"]; ok {
			out["path"] = value
		}
		if value, ok := inputMap["StartLine"]; ok {
			out["start_line"] = value
		}
		if value, ok := inputMap["EndLine"]; ok {
			out["end_line"] = value
		}
		if value, ok := inputMap["IsSkillFile"]; ok {
			out["is_skill_file"] = value
		}
		return out
	case "list_dir":
		if value, ok := inputMap["DirectoryPath"]; ok {
			return map[string]any{"path": value}
		}
	case "run_command":
		out := map[string]any{}
		if value, ok := inputMap["CommandLine"]; ok {
			out["command"] = value
		}
		if value, ok := inputMap["Cwd"]; ok {
			out["cwd"] = value
		}
		if value, ok := inputMap["WaitMsBeforeAsync"]; ok {
			out["waitMsBeforeAsync"] = value
		}
		if value, ok := inputMap["SafeToAutoRun"]; ok {
			out["safeToAutoRun"] = value
		}
		if value, ok := inputMap["RunPersistent"]; ok {
			out["runPersistent"] = value
		}
		if value, ok := inputMap["RequestedTerminalID"]; ok {
			out["requestedTerminalId"] = value
		}
		return out
	case "send_command_input":
		out := map[string]any{}
		if value, ok := inputMap["CommandId"]; ok {
			out["commandId"] = value
		}
		if value, ok := inputMap["Input"]; ok {
			out["data"] = value
		}
		if value, ok := inputMap["Terminate"]; ok {
			out["terminate"] = value
		}
		if value, ok := inputMap["WaitMs"]; ok {
			out["wait_ms"] = value
		}
		if value, ok := inputMap["SafeToAutoRun"]; ok {
			out["safeToAutoRun"] = value
		}
		return out
	case "search_web":
		return map[string]any{
			"query":  inputMap["query"],
			"domain": inputMap["domain"],
		}
	case "read_url_content":
		if value, ok := inputMap["Url"]; ok {
			return map[string]any{"url": value}
		}
	case "command_status":
		out := map[string]any{}
		if value, ok := inputMap["CommandId"]; ok {
			out["commandId"] = value
		}
		if value, ok := inputMap["WaitDurationSeconds"]; ok {
			out["waitDurationSeconds"] = value
		}
		if value, ok := inputMap["OutputCharacterCount"]; ok {
			out["outputCharacterCount"] = value
		}
		return out
	case "generate_image":
		out := map[string]any{}
		if value, ok := inputMap["Prompt"]; ok {
			out["prompt"] = value
		}
		if value, ok := inputMap["ImageName"]; ok {
			out["imageName"] = value
		}
		if value, ok := inputMap["ImagePaths"]; ok {
			out["imagePaths"] = value
		}
		return out
	}
	return inputMap
}
