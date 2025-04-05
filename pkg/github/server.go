package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/github/github-mcp-server/pkg/translations"
	"github.com/google/go-github/v69/github"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// NewServer creates a new GitHub MCP server with the specified GH client and logger.
func NewServer(client *github.Client, readOnly bool, t translations.TranslationHelperFunc, excludeTools string, includeTools string) *server.MCPServer {
	// Create a new MCP server
	s := server.NewMCPServer(
		"github-mcp-server",
		"0.0.1",
		server.WithResourceCapabilities(true, true),
		server.WithLogging())

	// Parse tool lists
	excludeList := make(map[string]bool)
	if excludeTools != "" {
		for _, tool := range strings.Split(excludeTools, ",") {
			excludeList[strings.TrimSpace(tool)] = true
		}
	}

	includeList := make(map[string]bool)
	if includeTools != "" {
		for _, tool := range strings.Split(includeTools, ",") {
			includeList[strings.TrimSpace(tool)] = true
		}
	}

	// Helper function to check if a tool should be included
	shouldIncludeTool := func(toolName string) bool {
		// If include list is not empty, only include tools in that list
		if len(includeList) > 0 {
			return includeList[toolName]
		}
		// Otherwise, include all tools except those in the exclude list
		return !excludeList[toolName]
	}

	// Helper function to add a tool if it should be included
	addToolIfIncluded := func(tool mcp.Tool, handler server.ToolHandlerFunc) {
		if shouldIncludeTool(tool.Name) {
			s.AddTool(tool, handler)
		}
	}

	// Add GitHub Resources
	s.AddResourceTemplate(getRepositoryResourceContent(client, t))
	s.AddResourceTemplate(getRepositoryResourceBranchContent(client, t))
	s.AddResourceTemplate(getRepositoryResourceCommitContent(client, t))
	s.AddResourceTemplate(getRepositoryResourceTagContent(client, t))
	s.AddResourceTemplate(getRepositoryResourcePrContent(client, t))

	// Add GitHub tools - Issues
	addToolIfIncluded(getIssue(client, t))
	addToolIfIncluded(searchIssues(client, t))
	addToolIfIncluded(listIssues(client, t))

	if !readOnly {
		addToolIfIncluded(createIssue(client, t))
		addToolIfIncluded(addIssueComment(client, t))
		addToolIfIncluded(updateIssue(client, t))
	}

	// Add GitHub tools - Pull Requests
	addToolIfIncluded(getPullRequest(client, t))
	addToolIfIncluded(listPullRequests(client, t))
	addToolIfIncluded(getPullRequestFiles(client, t))
	addToolIfIncluded(getPullRequestStatus(client, t))
	addToolIfIncluded(getPullRequestComments(client, t))
	addToolIfIncluded(getPullRequestReviews(client, t))
	if !readOnly {
		addToolIfIncluded(mergePullRequest(client, t))
		addToolIfIncluded(updatePullRequestBranch(client, t))
		addToolIfIncluded(createPullRequestReview(client, t))
		addToolIfIncluded(createPullRequest(client, t))
	}

	// Add GitHub tools - Repositories
	addToolIfIncluded(searchRepositories(client, t))
	addToolIfIncluded(getFileContents(client, t))
	addToolIfIncluded(listCommits(client, t))
	if !readOnly {
		addToolIfIncluded(createOrUpdateFile(client, t))
		addToolIfIncluded(createRepository(client, t))
		addToolIfIncluded(forkRepository(client, t))
		addToolIfIncluded(createBranch(client, t))
		addToolIfIncluded(pushFiles(client, t))
	}

	// Add GitHub tools - Search
	addToolIfIncluded(searchCode(client, t))
	addToolIfIncluded(searchUsers(client, t))

	// Add GitHub tools - Users
	addToolIfIncluded(getMe(client, t))

	// Add GitHub tools - Code Scanning
	addToolIfIncluded(getCodeScanningAlert(client, t))
	addToolIfIncluded(listCodeScanningAlerts(client, t))

	return s
}

// getMe creates a tool to get details of the authenticated user.
func getMe(client *github.Client, t translations.TranslationHelperFunc) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_me",
			mcp.WithDescription(t("TOOL_GET_ME_DESCRIPTION", "Get details of the authenticated GitHub user. Use this when a request include \"me\", \"my\"...")),
			mcp.WithString("reason",
				mcp.Description("Optional: reason the session was created"),
			),
		),
		func(ctx context.Context, _ mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			user, resp, err := client.Users.Get(ctx, "")
			if err != nil {
				return nil, fmt.Errorf("failed to get user: %w", err)
			}
			defer func() { _ = resp.Body.Close() }()

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get user: %s", string(body))), nil
			}

			r, err := json.Marshal(user)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal user: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// isAcceptedError checks if the error is an accepted error.
func isAcceptedError(err error) bool {
	var acceptedError *github.AcceptedError
	return errors.As(err, &acceptedError)
}

// requiredParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request.
// 2. Checks if the parameter is of the expected type.
// 3. Checks if the parameter is not empty, i.e: non-zero value
func requiredParam[T comparable](r mcp.CallToolRequest, p string) (T, error) {
	var zero T

	// Check if the parameter is present in the request
	if _, ok := r.Params.Arguments[p]; !ok {
		return zero, fmt.Errorf("missing required parameter: %s", p)
	}

	// Check if the parameter is of the expected type
	if _, ok := r.Params.Arguments[p].(T); !ok {
		return zero, fmt.Errorf("parameter %s is not of type %T", p, zero)
	}

	if r.Params.Arguments[p].(T) == zero {
		return zero, fmt.Errorf("missing required parameter: %s", p)

	}

	return r.Params.Arguments[p].(T), nil
}

// requiredInt is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request.
// 2. Checks if the parameter is of the expected type.
// 3. Checks if the parameter is not empty, i.e: non-zero value
func requiredInt(r mcp.CallToolRequest, p string) (int, error) {
	v, err := requiredParam[float64](r, p)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

// optionalParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request, if not, it returns its zero-value
// 2. If it is present, it checks if the parameter is of the expected type and returns it
func optionalParam[T any](r mcp.CallToolRequest, p string) (T, error) {
	var zero T

	// Check if the parameter is present in the request
	if _, ok := r.Params.Arguments[p]; !ok {
		return zero, nil
	}

	// Check if the parameter is of the expected type
	if _, ok := r.Params.Arguments[p].(T); !ok {
		return zero, fmt.Errorf("parameter %s is not of type %T, is %T", p, zero, r.Params.Arguments[p])
	}

	return r.Params.Arguments[p].(T), nil
}

// optionalIntParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request, if not, it returns its zero-value
// 2. If it is present, it checks if the parameter is of the expected type and returns it
func optionalIntParam(r mcp.CallToolRequest, p string) (int, error) {
	v, err := optionalParam[float64](r, p)
	if err != nil {
		return 0, err
	}
	return int(v), nil
}

// optionalIntParamWithDefault is a helper function that can be used to fetch a requested parameter from the request
// similar to optionalIntParam, but it also takes a default value.
func optionalIntParamWithDefault(r mcp.CallToolRequest, p string, d int) (int, error) {
	v, err := optionalIntParam(r, p)
	if err != nil {
		return 0, err
	}
	if v == 0 {
		return d, nil
	}
	return v, nil
}

// optionalStringArrayParam is a helper function that can be used to fetch a requested parameter from the request.
// It does the following checks:
// 1. Checks if the parameter is present in the request, if not, it returns its zero-value
// 2. If it is present, iterates the elements and checks each is a string
func optionalStringArrayParam(r mcp.CallToolRequest, p string) ([]string, error) {
	// Check if the parameter is present in the request
	if _, ok := r.Params.Arguments[p]; !ok {
		return []string{}, nil
	}

	switch v := r.Params.Arguments[p].(type) {
	case []string:
		return v, nil
	case []any:
		strSlice := make([]string, len(v))
		for i, v := range v {
			s, ok := v.(string)
			if !ok {
				return []string{}, fmt.Errorf("parameter %s is not of type string, is %T", p, v)
			}
			strSlice[i] = s
		}
		return strSlice, nil
	default:
		return []string{}, fmt.Errorf("parameter %s could not be coerced to []string, is %T", p, r.Params.Arguments[p])
	}
}
