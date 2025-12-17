// Package main implements the Jira plugin for Relicta.
package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	jira "github.com/felixgeelhaar/jirasdk"
	"github.com/felixgeelhaar/jirasdk/core/issue"
	"github.com/felixgeelhaar/jirasdk/core/project"

	"github.com/relicta-tech/relicta-plugin-sdk/plugin"
)

// JiraPlugin implements the Jira integration plugin.
type JiraPlugin struct{}

// Config represents the Jira plugin configuration.
type Config struct {
	// BaseURL is the Jira instance URL (e.g., https://company.atlassian.net).
	BaseURL string `json:"base_url,omitempty"`
	// Username is the Jira username (email for Atlassian Cloud).
	Username string `json:"username,omitempty"`
	// Token is the Jira API token (or password for on-premise).
	Token string `json:"token,omitempty"`
	// ProjectKey is the Jira project key (e.g., "PROJ").
	ProjectKey string `json:"project_key,omitempty"`
	// VersionName is the name for the Jira version/release (default: version string).
	VersionName string `json:"version_name,omitempty"`
	// VersionDescription is the description for the Jira version.
	VersionDescription string `json:"version_description,omitempty"`
	// CreateVersion creates a new version in Jira.
	CreateVersion bool `json:"create_version"`
	// ReleaseVersion marks the version as released.
	ReleaseVersion bool `json:"release_version"`
	// TransitionIssues transitions linked issues to a specified status.
	TransitionIssues bool `json:"transition_issues"`
	// TransitionName is the transition name to apply (e.g., "Done", "Closed", "Released").
	TransitionName string `json:"transition_name,omitempty"`
	// AddComment adds a comment to linked issues.
	AddComment bool `json:"add_comment"`
	// CommentTemplate is the comment template (supports {version}, {release_url} placeholders).
	CommentTemplate string `json:"comment_template,omitempty"`
	// IssuePattern is a regex pattern to extract issue keys from commits (default: project-\\d+).
	IssuePattern string `json:"issue_pattern,omitempty"`
	// AssociateIssues associates extracted issues with the version.
	AssociateIssues bool `json:"associate_issues"`
}

// GetInfo returns plugin metadata.
func (p *JiraPlugin) GetInfo() plugin.Info {
	return plugin.Info{
		Name:        "jira",
		Version:     "2.0.0",
		Description: "Integrate with Jira for version management and issue tracking",
		Author:      "Relicta Team",
		Hooks: []plugin.Hook{
			plugin.HookPostPlan,
			plugin.HookPostPublish,
			plugin.HookOnSuccess,
			plugin.HookOnError,
		},
		ConfigSchema: `{
			"type": "object",
			"properties": {
				"base_url": {"type": "string", "description": "Jira instance URL (e.g., https://company.atlassian.net)"},
				"username": {"type": "string", "description": "Jira username (email for Atlassian Cloud)"},
				"token": {"type": "string", "description": "Jira API token (or use JIRA_TOKEN env)"},
				"project_key": {"type": "string", "description": "Jira project key (e.g., 'PROJ')"},
				"version_name": {"type": "string", "description": "Version name (default: version string)"},
				"version_description": {"type": "string", "description": "Version description"},
				"create_version": {"type": "boolean", "description": "Create a new version in Jira", "default": true},
				"release_version": {"type": "boolean", "description": "Mark version as released", "default": true},
				"transition_issues": {"type": "boolean", "description": "Transition linked issues", "default": false},
				"transition_name": {"type": "string", "description": "Transition name (e.g., 'Done', 'Released')"},
				"add_comment": {"type": "boolean", "description": "Add comment to linked issues", "default": false},
				"comment_template": {"type": "string", "description": "Comment template with {version}, {release_url} placeholders"},
				"issue_pattern": {"type": "string", "description": "Regex pattern to extract issue keys"},
				"associate_issues": {"type": "boolean", "description": "Associate issues with the version", "default": true}
			},
			"required": ["base_url", "project_key"]
		}`,
	}
}

// Execute runs the plugin for a given hook.
func (p *JiraPlugin) Execute(ctx context.Context, req plugin.ExecuteRequest) (*plugin.ExecuteResponse, error) {
	cfg := p.parseConfig(req.Config)

	switch req.Hook {
	case plugin.HookPostPlan:
		return p.handlePostPlan(ctx, cfg, req.Context, req.DryRun)
	case plugin.HookPostPublish:
		return p.handlePostPublish(ctx, cfg, req.Context, req.DryRun)
	case plugin.HookOnSuccess:
		return &plugin.ExecuteResponse{
			Success: true,
			Message: "Release successful - Jira integration acknowledged",
		}, nil
	case plugin.HookOnError:
		return &plugin.ExecuteResponse{
			Success: true,
			Message: "Release failed - Jira integration acknowledged",
		}, nil
	default:
		return &plugin.ExecuteResponse{
			Success: true,
			Message: fmt.Sprintf("Hook %s not handled", req.Hook),
		}, nil
	}
}

// handlePostPlan handles the PostPlan hook - extract and report linked issues.
func (p *JiraPlugin) handlePostPlan(_ context.Context, cfg *Config, releaseCtx plugin.ReleaseContext, _ bool) (*plugin.ExecuteResponse, error) {
	// Extract issue keys from commits
	issueKeys := p.extractIssueKeys(cfg, releaseCtx.Changes)

	if len(issueKeys) == 0 {
		return &plugin.ExecuteResponse{
			Success: true,
			Message: "No Jira issues found in commits",
			Outputs: map[string]any{
				"issues_found": 0,
			},
		}, nil
	}

	return &plugin.ExecuteResponse{
		Success: true,
		Message: fmt.Sprintf("Found %d Jira issue(s) linked to this release: %s", len(issueKeys), strings.Join(issueKeys, ", ")),
		Outputs: map[string]any{
			"issues_found": len(issueKeys),
			"issue_keys":   issueKeys,
		},
	}, nil
}

// handlePostPublish handles the PostPublish hook - create/release version, update issues.
func (p *JiraPlugin) handlePostPublish(ctx context.Context, cfg *Config, releaseCtx plugin.ReleaseContext, dryRun bool) (*plugin.ExecuteResponse, error) {
	// Create Jira client
	client, err := p.getClient(cfg)
	if err != nil {
		return &plugin.ExecuteResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to create Jira client: %v", err),
		}, nil
	}

	versionName := cfg.VersionName
	if versionName == "" {
		versionName = releaseCtx.Version
	}

	// Extract issue keys from commits
	issueKeys := p.extractIssueKeys(cfg, releaseCtx.Changes)

	if dryRun {
		actions := []string{}
		if cfg.CreateVersion {
			actions = append(actions, fmt.Sprintf("Create version '%s' in project %s", versionName, cfg.ProjectKey))
		}
		if cfg.ReleaseVersion {
			actions = append(actions, fmt.Sprintf("Mark version '%s' as released", versionName))
		}
		if cfg.AssociateIssues && len(issueKeys) > 0 {
			actions = append(actions, fmt.Sprintf("Associate %d issues with version", len(issueKeys)))
		}
		if cfg.TransitionIssues && cfg.TransitionName != "" && len(issueKeys) > 0 {
			actions = append(actions, fmt.Sprintf("Transition %d issues to '%s'", len(issueKeys), cfg.TransitionName))
		}
		if cfg.AddComment && cfg.CommentTemplate != "" && len(issueKeys) > 0 {
			actions = append(actions, fmt.Sprintf("Add comment to %d issues", len(issueKeys)))
		}

		return &plugin.ExecuteResponse{
			Success: true,
			Message: fmt.Sprintf("Would perform: %s", strings.Join(actions, "; ")),
			Outputs: map[string]any{
				"version_name": versionName,
				"project_key":  cfg.ProjectKey,
				"issues":       issueKeys,
				"actions":      actions,
			},
		}, nil
	}

	var versionID string
	results := []string{}

	// Create version if requested
	if cfg.CreateVersion {
		version, err := p.createOrGetVersion(ctx, client, cfg.ProjectKey, versionName, cfg.VersionDescription)
		if err != nil {
			return &plugin.ExecuteResponse{
				Success: false,
				Error:   fmt.Sprintf("failed to create/get version: %v", err),
			}, nil
		}
		versionID = version.ID
		results = append(results, fmt.Sprintf("Created/found version '%s'", versionName))
	}

	// Release version if requested
	if cfg.ReleaseVersion && versionID != "" {
		err := p.releaseVersion(ctx, client, versionID)
		if err != nil {
			results = append(results, fmt.Sprintf("Failed to release version: %v", err))
		} else {
			results = append(results, fmt.Sprintf("Marked version '%s' as released", versionName))
		}
	}

	// Associate issues with version
	if cfg.AssociateIssues && versionID != "" && len(issueKeys) > 0 {
		successCount := 0
		for _, issueKey := range issueKeys {
			err := p.associateIssueWithVersion(ctx, client, issueKey, versionName)
			if err == nil {
				successCount++
			}
		}
		results = append(results, fmt.Sprintf("Associated %d/%d issues with version", successCount, len(issueKeys)))
	}

	// Transition issues
	if cfg.TransitionIssues && cfg.TransitionName != "" && len(issueKeys) > 0 {
		successCount := 0
		for _, issueKey := range issueKeys {
			err := p.transitionIssue(ctx, client, issueKey, cfg.TransitionName)
			if err == nil {
				successCount++
			}
		}
		results = append(results, fmt.Sprintf("Transitioned %d/%d issues to '%s'", successCount, len(issueKeys), cfg.TransitionName))
	}

	// Add comments to issues
	if cfg.AddComment && cfg.CommentTemplate != "" && len(issueKeys) > 0 {
		comment := p.buildComment(cfg.CommentTemplate, releaseCtx)
		successCount := 0
		for _, issueKey := range issueKeys {
			err := p.addComment(ctx, client, issueKey, comment)
			if err == nil {
				successCount++
			}
		}
		results = append(results, fmt.Sprintf("Added comments to %d/%d issues", successCount, len(issueKeys)))
	}

	return &plugin.ExecuteResponse{
		Success: true,
		Message: strings.Join(results, "; "),
		Outputs: map[string]any{
			"version_name": versionName,
			"version_id":   versionID,
			"project_key":  cfg.ProjectKey,
			"issues":       issueKeys,
		},
	}, nil
}

// extractIssueKeys extracts Jira issue keys from commit messages.
func (p *JiraPlugin) extractIssueKeys(cfg *Config, changes *plugin.CategorizedChanges) []string {
	pattern := cfg.IssuePattern
	if pattern == "" {
		// Default pattern: PROJECT-123 (project key followed by hyphen and digits)
		pattern = `[A-Z][A-Z0-9]*-\d+`
	}

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil
	}

	seen := make(map[string]bool)
	var keys []string

	// Helper function to extract from a slice of commits
	extractFromCommits := func(commits []plugin.ConventionalCommit) {
		for _, commit := range commits {
			// Check description
			matches := re.FindAllString(commit.Description, -1)
			for _, match := range matches {
				upperMatch := strings.ToUpper(match)
				if !seen[upperMatch] {
					seen[upperMatch] = true
					keys = append(keys, upperMatch)
				}
			}
			// Also check body if present
			if commit.Body != "" {
				bodyMatches := re.FindAllString(commit.Body, -1)
				for _, match := range bodyMatches {
					upperMatch := strings.ToUpper(match)
					if !seen[upperMatch] {
						seen[upperMatch] = true
						keys = append(keys, upperMatch)
					}
				}
			}
			// Also extract from referenced issues in the commit
			for _, iss := range commit.Issues {
				upperMatch := strings.ToUpper(iss)
				if !seen[upperMatch] && re.MatchString(upperMatch) {
					seen[upperMatch] = true
					keys = append(keys, upperMatch)
				}
			}
		}
	}

	if changes != nil {
		extractFromCommits(changes.Features)
		extractFromCommits(changes.Fixes)
		extractFromCommits(changes.Breaking)
		extractFromCommits(changes.Performance)
		extractFromCommits(changes.Refactor)
		extractFromCommits(changes.Docs)
		extractFromCommits(changes.Other)
	}

	return keys
}

// createOrGetVersion creates a new version or returns existing one.
func (p *JiraPlugin) createOrGetVersion(ctx context.Context, client *jira.Client, projectKey, versionName, description string) (*project.Version, error) {
	// Try to find existing version first by listing project versions
	versions, err := client.Project.ListProjectVersions(ctx, projectKey)
	if err != nil {
		return nil, fmt.Errorf("failed to list project versions: %w", err)
	}

	for _, v := range versions {
		if v.Name == versionName {
			return v, nil
		}
	}

	// Create new version using jirasdk
	createdVersion, err := client.Project.CreateVersion(ctx, &project.CreateVersionInput{
		Name:        versionName,
		Description: description,
		Project:     projectKey,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create version: %w", err)
	}

	return createdVersion, nil
}

// releaseVersion marks a version as released.
func (p *JiraPlugin) releaseVersion(ctx context.Context, client *jira.Client, versionID string) error {
	now := time.Now().Format("2006-01-02")
	released := true

	_, err := client.Project.UpdateVersion(ctx, versionID, &project.UpdateVersionInput{
		Released:    &released,
		ReleaseDate: now,
	})
	return err
}

// associateIssueWithVersion adds a fix version to an issue.
func (p *JiraPlugin) associateIssueWithVersion(ctx context.Context, client *jira.Client, issueKey, versionName string) error {
	// Use jirasdk's Issue.Update with fixVersions field
	return client.Issue.Update(ctx, issueKey, &issue.UpdateInput{
		Fields: map[string]interface{}{
			"fixVersions": []map[string]string{
				{"name": versionName},
			},
		},
	})
}

// transitionIssue transitions an issue to a specified status.
func (p *JiraPlugin) transitionIssue(ctx context.Context, client *jira.Client, issueKey, transitionName string) error {
	// Get available transitions for the issue
	transitions, err := client.Workflow.GetTransitions(ctx, issueKey, nil)
	if err != nil {
		return fmt.Errorf("failed to get transitions: %w", err)
	}

	var transitionID string
	lowerName := strings.ToLower(transitionName)
	for _, t := range transitions {
		if strings.ToLower(t.Name) == lowerName {
			transitionID = t.ID
			break
		}
	}

	if transitionID == "" {
		return fmt.Errorf("transition '%s' not found for issue %s", transitionName, issueKey)
	}

	// Perform the transition using jirasdk's Issue.DoTransition
	return client.Issue.DoTransition(ctx, issueKey, &issue.TransitionInput{
		Transition: &issue.Transition{ID: transitionID},
	})
}

// addComment adds a comment to an issue.
func (p *JiraPlugin) addComment(ctx context.Context, client *jira.Client, issueKey, body string) error {
	// Create ADF (Atlassian Document Format) from plain text
	adf := &issue.ADF{
		Version: 1,
		Type:    "doc",
		Content: []issue.ADFNode{
			{
				Type: "paragraph",
				Content: []issue.ADFNode{
					{Type: "text", Text: body},
				},
			},
		},
	}
	_, err := client.Issue.AddComment(ctx, issueKey, &issue.AddCommentInput{
		Body: adf,
	})
	return err
}

// buildComment builds a comment from template.
func (p *JiraPlugin) buildComment(template string, releaseCtx plugin.ReleaseContext) string {
	comment := template
	comment = strings.ReplaceAll(comment, "{version}", releaseCtx.Version)
	comment = strings.ReplaceAll(comment, "{tag}", releaseCtx.TagName)
	comment = strings.ReplaceAll(comment, "{release_url}", releaseCtx.RepositoryURL)
	comment = strings.ReplaceAll(comment, "{repository}", releaseCtx.RepositoryName)
	return comment
}

// validateBaseURL validates the Jira base URL to prevent SSRF attacks.
func validateBaseURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("base URL is required")
	}

	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Check scheme - require HTTPS for production
	if parsedURL.Scheme != "https" {
		// Allow HTTP only for localhost (development)
		if parsedURL.Scheme == "http" {
			host := parsedURL.Hostname()
			if host != "localhost" && host != "127.0.0.1" && !strings.HasPrefix(host, "localhost:") {
				return fmt.Errorf("base_url must use HTTPS for non-localhost URLs")
			}
		} else {
			return fmt.Errorf("base_url must use https:// scheme")
		}
	}

	// Check for control characters and newlines that could enable request smuggling
	if strings.ContainsAny(rawURL, "\r\n\t") {
		return fmt.Errorf("base_url contains invalid control characters")
	}

	// Check for common SSRF bypasses
	host := parsedURL.Hostname()

	// Deny localhost/loopback (except in development with explicit localhost)
	if parsedURL.Scheme == "https" {
		if host == "localhost" || host == "127.0.0.1" || host == "[::1]" {
			return fmt.Errorf("base_url cannot point to localhost")
		}
	}

	// Resolve hostname and check for private IP addresses
	ips, err := net.LookupIP(host)
	if err == nil {
		for _, ip := range ips {
			if isPrivateIP(ip) {
				return fmt.Errorf("base_url resolves to private/internal IP address (%s)", ip.String())
			}
		}
	}

	// Check for cloud metadata endpoints (common SSRF targets)
	metadataHosts := []string{
		"169.254.169.254",
		"metadata.google.internal",
		"metadata.goog",
		"100.100.100.200",
		"fd00:ec2::254",
	}
	for _, metaHost := range metadataHosts {
		if strings.EqualFold(host, metaHost) {
			return fmt.Errorf("base_url cannot point to cloud metadata service")
		}
	}

	return nil
}

// isPrivateIP checks if an IP address is private/internal.
func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// Check private IPv4 ranges
	privateBlocks := []string{
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		"127.0.0.0/8",
		"169.254.0.0/16",
		"100.64.0.0/10",
		"192.0.0.0/24",
		"192.0.2.0/24",
		"198.51.100.0/24",
		"203.0.113.0/24",
		"240.0.0.0/4",
	}

	for _, block := range privateBlocks {
		_, cidr, err := net.ParseCIDR(block)
		if err != nil {
			continue
		}
		if cidr.Contains(ip) {
			return true
		}
	}

	// Check private IPv6 ranges
	if ip.To4() == nil { // IPv6
		// fc00::/7 - Unique Local Addresses
		if ip[0] == 0xfc || ip[0] == 0xfd {
			return true
		}
		// fe80::/10 - Link-Local
		if ip[0] == 0xfe && (ip[1]&0xc0) == 0x80 {
			return true
		}
	}

	return false
}

// getClient creates a Jira client using jirasdk.
func (p *JiraPlugin) getClient(cfg *Config) (*jira.Client, error) {
	baseURL := cfg.BaseURL
	if baseURL == "" {
		return nil, fmt.Errorf("jira base URL is required")
	}

	// Validate URL for SSRF protection
	if err := validateBaseURL(baseURL); err != nil {
		return nil, fmt.Errorf("base_url validation failed: %w", err)
	}

	// Ensure URL doesn't have trailing slash
	baseURL = strings.TrimSuffix(baseURL, "/")

	username := cfg.Username
	if username == "" {
		username = os.Getenv("JIRA_USERNAME")
	}
	if username == "" {
		username = os.Getenv("JIRA_EMAIL")
	}

	token := cfg.Token
	if token == "" {
		token = os.Getenv("JIRA_TOKEN")
	}
	if token == "" {
		token = os.Getenv("JIRA_API_TOKEN")
	}

	if username == "" || token == "" {
		return nil, fmt.Errorf("jira username and token are required (set JIRA_USERNAME/JIRA_EMAIL and JIRA_TOKEN/JIRA_API_TOKEN env vars or configure in plugin)")
	}

	// Create client using jirasdk's functional options pattern
	client, err := jira.NewClient(
		jira.WithBaseURL(baseURL),
		jira.WithAPIToken(username, token),
		jira.WithTimeout(30*time.Second),
		jira.WithMaxRetries(3),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Jira client: %w", err)
	}

	return client, nil
}

// parseConfig parses the plugin configuration.
func (p *JiraPlugin) parseConfig(raw map[string]any) *Config {
	cfg := &Config{
		CreateVersion:   true,
		ReleaseVersion:  true,
		AssociateIssues: true,
	}

	if v, ok := raw["base_url"].(string); ok {
		cfg.BaseURL = v
	}
	if v, ok := raw["username"].(string); ok {
		cfg.Username = v
	}
	if v, ok := raw["token"].(string); ok {
		cfg.Token = v
	}
	if v, ok := raw["project_key"].(string); ok {
		cfg.ProjectKey = v
	}
	if v, ok := raw["version_name"].(string); ok {
		cfg.VersionName = v
	}
	if v, ok := raw["version_description"].(string); ok {
		cfg.VersionDescription = v
	}
	if v, ok := raw["create_version"].(bool); ok {
		cfg.CreateVersion = v
	}
	if v, ok := raw["release_version"].(bool); ok {
		cfg.ReleaseVersion = v
	}
	if v, ok := raw["transition_issues"].(bool); ok {
		cfg.TransitionIssues = v
	}
	if v, ok := raw["transition_name"].(string); ok {
		cfg.TransitionName = v
	}
	if v, ok := raw["add_comment"].(bool); ok {
		cfg.AddComment = v
	}
	if v, ok := raw["comment_template"].(string); ok {
		cfg.CommentTemplate = v
	}
	if v, ok := raw["issue_pattern"].(string); ok {
		cfg.IssuePattern = v
	}
	if v, ok := raw["associate_issues"].(bool); ok {
		cfg.AssociateIssues = v
	}

	return cfg
}

// Validate validates the plugin configuration.
func (p *JiraPlugin) Validate(_ context.Context, config map[string]any) (*plugin.ValidateResponse, error) {
	var errors []plugin.ValidationError

	// Base URL is required
	baseURL := ""
	if v, ok := config["base_url"].(string); ok {
		baseURL = v
	}
	if baseURL == "" {
		errors = append(errors, plugin.ValidationError{
			Field:   "base_url",
			Message: "Jira base URL is required",
			Code:    "required",
		})
	} else if !strings.HasPrefix(baseURL, "https://") && !strings.HasPrefix(baseURL, "http://") {
		errors = append(errors, plugin.ValidationError{
			Field:   "base_url",
			Message: "base_url must start with http:// or https://",
			Code:    "format",
		})
	}

	// Project key is required
	projectKey := ""
	if v, ok := config["project_key"].(string); ok {
		projectKey = v
	}
	if projectKey == "" {
		errors = append(errors, plugin.ValidationError{
			Field:   "project_key",
			Message: "Jira project key is required",
			Code:    "required",
		})
	}

	// Token/credentials check
	token := ""
	if v, ok := config["token"].(string); ok {
		token = v
	}
	if token == "" {
		token = os.Getenv("JIRA_TOKEN")
	}
	if token == "" {
		token = os.Getenv("JIRA_API_TOKEN")
	}

	username := ""
	if v, ok := config["username"].(string); ok {
		username = v
	}
	if username == "" {
		username = os.Getenv("JIRA_USERNAME")
	}
	if username == "" {
		username = os.Getenv("JIRA_EMAIL")
	}

	if token == "" {
		errors = append(errors, plugin.ValidationError{
			Field:   "token",
			Message: "Jira API token is required (set JIRA_TOKEN env var or configure token)",
			Code:    "required",
		})
	}
	if username == "" {
		errors = append(errors, plugin.ValidationError{
			Field:   "username",
			Message: "Jira username is required (set JIRA_USERNAME env var or configure username)",
			Code:    "required",
		})
	}

	// Validate issue pattern if provided
	if pattern, ok := config["issue_pattern"].(string); ok && pattern != "" {
		_, err := regexp.Compile(pattern)
		if err != nil {
			errors = append(errors, plugin.ValidationError{
				Field:   "issue_pattern",
				Message: fmt.Sprintf("Invalid regex pattern: %v", err),
				Code:    "format",
			})
		}
	}

	// Validate transition_name is provided when transition_issues is true
	if transitionIssues, ok := config["transition_issues"].(bool); ok && transitionIssues {
		transitionName := ""
		if v, ok := config["transition_name"].(string); ok {
			transitionName = v
		}
		if transitionName == "" {
			errors = append(errors, plugin.ValidationError{
				Field:   "transition_name",
				Message: "transition_name is required when transition_issues is true",
				Code:    "required",
			})
		}
	}

	// Validate comment_template is provided when add_comment is true
	if addComment, ok := config["add_comment"].(bool); ok && addComment {
		commentTemplate := ""
		if v, ok := config["comment_template"].(string); ok {
			commentTemplate = v
		}
		if commentTemplate == "" {
			errors = append(errors, plugin.ValidationError{
				Field:   "comment_template",
				Message: "comment_template is required when add_comment is true",
				Code:    "required",
			})
		}
	}

	return &plugin.ValidateResponse{
		Valid:  len(errors) == 0,
		Errors: errors,
	}, nil
}
