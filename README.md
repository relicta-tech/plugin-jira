# Relicta Jira Plugin

Official Jira plugin for [Relicta](https://github.com/relicta-tech/relicta) - AI-powered release management.

## Features

- Create and manage Jira versions
- Extract issue keys from commit messages
- Associate issues with release versions
- Transition issues on release
- Add comments to linked issues
- Atlassian Cloud and Server/Data Center support

## Installation

```bash
relicta plugin install jira
relicta plugin enable jira
```

## Configuration

Add to your `release.config.yaml`:

```yaml
plugins:
  - name: jira
    enabled: true
    config:
      base_url: "https://company.atlassian.net"
      project_key: "PROJ"
      create_version: true
      release_version: true
      associate_issues: true
      transition_issues: true
      transition_name: "Done"
      add_comment: true
      comment_template: "Released in version {version}"
```

### Environment Variables

- `JIRA_USERNAME` or `JIRA_EMAIL` - Jira username (required)
- `JIRA_TOKEN` or `JIRA_API_TOKEN` - Jira API token (required)

### Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `base_url` | Jira instance URL | Required |
| `username` | Jira username | - |
| `token` | Jira API token | - |
| `project_key` | Jira project key | Required |
| `version_name` | Version name | Release version |
| `version_description` | Version description | - |
| `create_version` | Create Jira version | `true` |
| `release_version` | Mark version as released | `true` |
| `transition_issues` | Transition linked issues | `false` |
| `transition_name` | Transition name (e.g., "Done") | - |
| `add_comment` | Add comment to issues | `false` |
| `comment_template` | Comment template | - |
| `issue_pattern` | Regex for issue keys | `[A-Z][A-Z0-9]*-\d+` |
| `associate_issues` | Associate issues with version | `true` |

### Comment Template Placeholders

- `{version}` - Release version
- `{tag}` - Git tag name
- `{release_url}` - Repository URL
- `{repository}` - Repository name

## API Token

For Atlassian Cloud, create an API token at:
https://id.atlassian.com/manage-profile/security/api-tokens

## Hooks

This plugin responds to the following hooks:

- `post_plan` - Extracts and reports linked Jira issues
- `post_publish` - Creates version, updates issues
- `on_success` - Acknowledges successful release
- `on_error` - Acknowledges failed release

## Development

```bash
# Build
go build -o jira .

# Test locally
relicta plugin install ./jira
relicta plugin enable jira
relicta publish --dry-run
```

## License

MIT License - see [LICENSE](LICENSE) for details.
