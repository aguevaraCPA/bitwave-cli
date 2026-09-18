package operations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

type apiRequestFlags struct {
	service     string
	orgID       string
	input       string
	data        string
	bodyFile    string
	contentType string
	query       []string
	headers     []string
	out         string
	yes         bool
	dryRun      bool
	readOnly    bool
}

type graphqlFlags struct {
	service       string
	orgID         string
	query         string
	queryFile     string
	variables     string
	operationName string
	out           string
	yes           bool
	dryRun        bool
}

func newAPICmd() *op.Definition {
	cmd := &op.Definition{
		Use:   "api",
		Short: "Call authenticated Bitwave API and GraphQL endpoints",
		Long: `Call Bitwave backend operations that do not yet have a typed CLI command.

Only relative paths on known Bitwave services are accepted. The command uses
the active organization and the same automatically refreshed authentication as
the rest of the CLI. Use {org} in a path to insert the selected organization ID.`,
	}
	cmd.AddCommand(newAPIRequestCmd())
	cmd.AddCommand(newAPIGraphQLCmd())
	return cmd
}

func newAPIRequestCmd() *op.Definition {
	var f apiRequestFlags
	cmd := &op.Definition{
		Use:   "request METHOD PATH",
		Short: "Send an authenticated request to a Bitwave API service",
		Args:  op.ExactArgs(2),
		RunE: func(cmd *op.Call, args []string) error {
			return runAPIRequest(cmd, args[0], args[1], f)
		},
	}
	cmd.Flags().StringVar(&f.service, "service", orgreports.APIServiceCore, "Bitwave service: core, api2, app, platform, reports, or transactions")
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVarP(&f.input, "input", "i", "", "JSON request body file (`-` reads stdin)")
	cmd.Flags().StringVar(&f.data, "data", "", "Inline JSON request body")
	cmd.Flags().StringVar(&f.bodyFile, "body-file", "", "Exact request body file (`-` reads stdin)")
	cmd.Flags().StringVar(&f.contentType, "content-type", "", "Request content type (defaults to JSON or application/octet-stream)")
	cmd.Flags().StringArrayVar(&f.query, "query", nil, "Query parameter as key=value (repeatable)")
	cmd.Flags().StringArrayVar(&f.headers, "header", nil, "Additional header as name=value (repeatable; authentication headers are refused)")
	cmd.Flags().StringVarP(&f.out, "out", "o", "", "Save the response to a file instead of stdout")
	cmd.Flags().BoolVar(&f.yes, "yes", false, "Confirm a request that may change the organization")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Print the exact request without contacting Bitwave")
	cmd.Flags().BoolVar(&f.readOnly, "read-only", false, "Assert that a non-GET endpoint is read-only")
	return cmd
}

func runAPIRequest(cmd *op.Call, method, path string, f apiRequestFlags) error {
	method = strings.ToUpper(strings.TrimSpace(method))
	if !validHTTPMethod(method) {
		return fmt.Errorf("unsupported HTTP method %q", method)
	}
	if countNonEmpty(f.input, f.data, f.bodyFile) > 1 {
		return errors.New("use only one of --input, --data, or --body-file")
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return err
	}
	path = strings.ReplaceAll(path, "{org}", url.PathEscape(orgID))
	path, err = appendAPIQuery(path, f.query)
	if err != nil {
		return err
	}
	body, isJSON, err := readAPIRequestBody(cmd.Context(), f.input, f.data, f.bodyFile, cmd.InOrStdin())
	if err != nil {
		return err
	}
	headers, err := parseAPIHeaders(f.headers)
	if err != nil {
		return err
	}
	if body != nil {
		contentType := f.contentType
		if contentType == "" && isJSON {
			contentType = "application/json"
		}
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		headers.Set("Content-Type", contentType)
	} else if f.contentType != "" {
		headers.Set("Content-Type", f.contentType)
	}
	client := newReportsClient(cmd.Context(), orgID)
	endpoint, err := client.RawEndpoint(f.service, path)
	if err != nil {
		return err
	}
	preview := apiPreview(method, endpoint, f.service, body)
	if len(headers) > 0 {
		preview["headers"] = headers
	}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: "api-request", Organization: orgID, DryRun: true, Request: preview})
	}
	if apiMethodMayWrite(method) && !f.readOnly && !f.yes {
		return errors.New("refusing a request that may change the organization without --yes (or --read-only for a read-only POST)")
	}
	data, err := client.RawRequestBytes(cmd.Context(), f.service, method, path, body, headers)
	if err != nil {
		return fmt.Errorf("Bitwave API request: %w", err)
	}
	return writeAPIResponse(cmd, f.out, data)
}

func newAPIGraphQLCmd() *op.Definition {
	var f graphqlFlags
	cmd := &op.Definition{
		Use:   "graphql",
		Short: "Run an authenticated Bitwave GraphQL operation",
		RunE: func(cmd *op.Call, _ []string) error {
			return runAPIGraphQL(cmd, f)
		},
	}
	cmd.Flags().StringVar(&f.service, "service", orgreports.APIServiceApp, "Bitwave GraphQL service: app, reports, or core")
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVar(&f.query, "query", "", "Inline GraphQL document")
	cmd.Flags().StringVar(&f.queryFile, "query-file", "", "GraphQL document file (`-` reads stdin)")
	cmd.Flags().StringVar(&f.variables, "variables", "", "JSON variables file (`-` reads stdin)")
	cmd.Flags().StringVar(&f.operationName, "operation", "", "GraphQL operation name")
	cmd.Flags().StringVarP(&f.out, "out", "o", "", "Save the response to a file instead of stdout")
	cmd.Flags().BoolVar(&f.yes, "yes", false, "Confirm a GraphQL mutation")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Print the exact request without contacting Bitwave")
	return cmd
}

func runAPIGraphQL(cmd *op.Call, f graphqlFlags) error {
	if (f.query == "") == (f.queryFile == "") {
		return errors.New("use exactly one of --query or --query-file")
	}
	if f.queryFile == "-" && f.variables == "-" {
		return errors.New("GraphQL query and variables cannot both read stdin")
	}
	document := f.query
	if f.queryFile != "" {
		data, err := readLimitedInput(cmd.Context(), f.queryFile, cmd.InOrStdin(), 4<<20)
		if err != nil {
			return fmt.Errorf("read GraphQL query: %w", err)
		}
		document = string(data)
	}
	if strings.TrimSpace(document) == "" {
		return errors.New("GraphQL query is empty")
	}
	variables := json.RawMessage(`{}`)
	if f.variables != "" {
		data, err := readLimitedInput(cmd.Context(), f.variables, cmd.InOrStdin(), 4<<20)
		if err != nil {
			return fmt.Errorf("read GraphQL variables: %w", err)
		}
		if !json.Valid(data) || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
			return errors.New("GraphQL variables must be a JSON object")
		}
		variables = data
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return err
	}
	payload := map[string]any{"query": document, "variables": variables}
	if f.operationName != "" {
		payload["operationName"] = f.operationName
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	path, err := graphQLEndpointPath(f.service)
	if err != nil {
		return err
	}
	client := newReportsClient(cmd.Context(), orgID)
	endpoint, err := client.RawEndpoint(f.service, path)
	if err != nil {
		return err
	}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: "graphql", Organization: orgID, DryRun: true, Request: apiPreview(http.MethodPost, endpoint, f.service, body)})
	}
	if graphqlMayWrite(document) && !f.yes {
		return errors.New("refusing a GraphQL mutation without --yes")
	}
	data, err := client.RawRequest(cmd.Context(), f.service, http.MethodPost, path, json.RawMessage(body))
	if err != nil {
		return fmt.Errorf("Bitwave GraphQL request: %w", err)
	}
	if err := writeAPIResponse(cmd, f.out, data); err != nil {
		return err
	}
	if graphQLError := graphqlResponseError(data); graphQLError != nil {
		return graphQLError
	}
	return nil
}

func validHTTPMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func apiMethodMayWrite(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func appendAPIQuery(path string, values []string) (string, error) {
	parsed, err := url.Parse(path)
	if err != nil {
		return "", fmt.Errorf("invalid API path: %w", err)
	}
	query := parsed.Query()
	for _, value := range values {
		key, item, ok := strings.Cut(value, "=")
		if !ok || strings.TrimSpace(key) == "" {
			return "", fmt.Errorf("invalid --query %q; expected key=value", value)
		}
		query.Add(strings.TrimSpace(key), item)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func readAPIRequestBody(ctx context.Context, path, inline, bodyFile string, stdin io.Reader) ([]byte, bool, error) {
	if path == "" && inline == "" && bodyFile == "" {
		return nil, false, nil
	}
	var data []byte
	var err error
	if inline != "" {
		data = []byte(inline)
	} else if bodyFile != "" {
		data, err = readLimitedInput(ctx, bodyFile, stdin, 64<<20)
		if err != nil {
			return nil, false, fmt.Errorf("read API body: %w", err)
		}
		return data, false, nil
	} else {
		data, err = readLimitedInput(ctx, path, stdin, 4<<20)
		if err != nil {
			return nil, false, fmt.Errorf("read API input: %w", err)
		}
	}
	if !json.Valid(data) {
		return nil, false, errors.New("API input must be valid JSON")
	}
	return data, true, nil
}

func countNonEmpty(values ...string) int {
	count := 0
	for _, value := range values {
		if value != "" {
			count++
		}
	}
	return count
}

func parseAPIHeaders(values []string) (http.Header, error) {
	headers := make(http.Header)
	blocked := map[string]bool{"Authorization": true, "Proxy-Authorization": true, "Cookie": true, "Host": true, "Content-Length": true}
	for _, value := range values {
		name, item, ok := strings.Cut(value, "=")
		name = http.CanonicalHeaderKey(strings.TrimSpace(name))
		if !ok || name == "" {
			return nil, fmt.Errorf("invalid --header %q; expected name=value", value)
		}
		if blocked[name] {
			return nil, fmt.Errorf("header %s is managed by the CLI and cannot be overridden", name)
		}
		headers.Add(name, item)
	}
	return headers, nil
}

func readLimitedInput(ctx context.Context, path string, stdin io.Reader, limit int64) ([]byte, error) {
	if path == "-" {
		return readAtMost(stdin, limit)
	}
	file, err := op.RuntimeFrom(ctx).Files.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	return readAtMost(file, limit)
}

func readAtMost(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("input exceeds %d bytes", limit)
	}
	return data, nil
}

func apiPreview(method, endpoint, service string, body []byte) map[string]any {
	preview := map[string]any{"method": method, "service": service, "url": endpoint}
	if body != nil {
		preview["bodyBytes"] = len(body)
		var decoded any
		if json.Unmarshal(body, &decoded) == nil {
			preview["body"] = decoded
		}
	}
	return preview
}

func graphqlResponseError(data []byte) error {
	var response struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if json.Unmarshal(data, &response) != nil || len(response.Errors) == 0 {
		return nil
	}
	messages := make([]string, 0, len(response.Errors))
	for _, item := range response.Errors {
		if item.Message != "" {
			messages = append(messages, item.Message)
		}
	}
	if len(messages) == 0 {
		return fmt.Errorf("Bitwave GraphQL returned %d error(s)", len(response.Errors))
	}
	return fmt.Errorf("Bitwave GraphQL: %s", strings.Join(messages, "; "))
}

func graphQLEndpointPath(service string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(service)) {
	case orgreports.APIServiceApp, orgreports.APIServiceCore, orgreports.APIServicePlatform:
		return "/graphql", nil
	case orgreports.APIServiceReports:
		return "/graphql-reports", nil
	default:
		return "", fmt.Errorf("unsupported GraphQL service %q (use app, platform, reports, or core)", service)
	}
}

func graphqlMayWrite(document string) bool {
	for _, line := range strings.Split(document, "\n") {
		if index := strings.Index(line, "#"); index >= 0 {
			line = line[:index]
		}
		fields := strings.FieldsFunc(line, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_'
		})
		for _, field := range fields {
			if strings.EqualFold(field, "mutation") {
				return true
			}
		}
	}
	return false
}

func writeAPIResponse(cmd *op.Call, out string, data []byte) error {
	if out != "" && out != "-" {
		if err := writeFileAtomic(cmd.Context(), out, data); err != nil {
			return fmt.Errorf("save API response: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "saved=%s\n", out)
		return nil
	}
	if _, err := cmd.OutOrStdout().Write(data); err != nil {
		return err
	}
	if len(data) > 0 && data[len(data)-1] != '\n' {
		_, err := fmt.Fprintln(cmd.OutOrStdout())
		return err
	}
	return nil
}
