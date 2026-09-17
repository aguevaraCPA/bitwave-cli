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
	"sort"
	"strings"

	op "github.com/bitwave-io/bitwave-cli/internal/operation"

	"github.com/bitwave-io/bitwave-cli/internal/orgreports"
)

type adminProtocol string

const (
	adminREST    adminProtocol = "rest"
	adminGraphQL adminProtocol = "graphql"
)

// adminOperation is the contract between one discoverable CLI action and the
// backend request used by the corresponding Admin UI action. Keeping the
// catalog declarative makes coverage auditable and keeps confirmation, org
// scoping, ETag handling, and structured output identical across providers.
type adminOperation struct {
	Area          string
	Name          string
	Use           string
	Short         string
	Protocol      adminProtocol
	Service       string
	Method        string
	Path          string
	Document      string
	ArgumentNames []string
	Defaults      map[string]any
	DefaultQuery  []string
	InputRequired bool
	AutoETag      bool
	FeatureFlag   string
	Notes         string
}

type adminOperationFlags struct {
	orgID  string
	input  string
	data   string
	query  []string
	etag   string
	yes    bool
	dryRun bool
	out    string
}

func newOrgAdminCmd() *op.Definition {
	cmd := &op.Definition{
		Use:   "admin",
		Short: "Operate the same organization administration surfaces as the Bitwave Admin UI",
		Long: `Operate Bitwave organization administration without relying on browser-only controls.

Commands mirror the backend contracts used by the Admin UI. Reads and writes
emit structured JSON. Every mutation supports --dry-run and requires --yes.
Organization ETags are loaded automatically for settings that use optimistic
concurrency. Feature-gated commands remain visible and return the backend's
permission or feature error when unavailable for the selected organization.`,
	}
	cmd.AddCommand(newOrgAdminCapabilitiesCmd())

	areas := map[string]*op.Definition{}
	for _, operation := range adminOperations() {
		area := areas[operation.Area]
		if area == nil {
			area = &op.Definition{Use: operation.Area, Short: adminAreaDescription(operation.Area)}
			areas[operation.Area] = area
			cmd.AddCommand(area)
		}
		area.AddCommand(newAdminOperationCmd(operation))
	}
	return cmd
}

func newOrgAdminCapabilitiesCmd() *op.Definition {
	var area string
	var jsonOutput bool
	cmd := &op.Definition{
		Use:   "capabilities",
		Short: "List every first-class Admin operation exposed by this CLI",
		RunE: func(cmd *op.Call, _ []string) error {
			operations := adminOperations()
			items := make([]map[string]any, 0, len(operations))
			for _, operation := range operations {
				if area != "" && operation.Area != area {
					continue
				}
				items = append(items, map[string]any{
					"area": operation.Area, "operation": operation.Name,
					"command":     "bitwave org admin " + operation.Area + " " + operation.Use,
					"description": operation.Short, "protocol": operation.Protocol,
					"featureFlag": operation.FeatureFlag, "notes": operation.Notes,
				})
			}
			if jsonOutput {
				return writeJSON(cmd.OutOrStdout(), map[string]any{"schemaVersion": "1", "operations": items, "total": len(items)})
			}
			for _, item := range items {
				fmt.Fprintf(cmd.OutOrStdout(), "%-22s %-34s %s\n", item["area"], item["operation"], item["description"])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&area, "area", "", "Limit results to one Admin area")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "Emit structured JSON")
	return cmd
}

func newAdminOperationCmd(operation adminOperation) *op.Definition {
	var f adminOperationFlags
	cmd := &op.Definition{
		Use:   operation.Use,
		Short: operation.Short,
		Args:  op.ExactArgs(len(operation.ArgumentNames)),
		RunE: func(cmd *op.Call, args []string) error {
			return runAdminOperation(cmd, operation, args, f)
		},
	}
	cmd.Flags().StringVar(&f.orgID, "org", "", "Organization ID override")
	cmd.Flags().StringVarP(&f.input, "input", "i", "", "JSON request/variables file (`-` reads stdin)")
	cmd.Flags().StringVar(&f.data, "data", "", "Inline JSON request/variables object")
	cmd.Flags().StringArrayVar(&f.query, "query", nil, "REST query parameter as key=value (repeatable)")
	cmd.Flags().StringVar(&f.etag, "etag", "auto", "Organization ETag for protected settings (`auto` loads it)")
	cmd.Flags().BoolVar(&f.yes, "yes", false, "Confirm an operation that changes the organization")
	cmd.Flags().BoolVar(&f.dryRun, "dry-run", false, "Print the exact request without contacting Bitwave")
	cmd.Flags().StringVarP(&f.out, "out", "o", "", "Save JSON response to a file")
	return cmd
}

func runAdminOperation(cmd *op.Call, operation adminOperation, args []string, f adminOperationFlags) error {
	if f.input != "" && f.data != "" {
		return errors.New("use only one of --input or --data")
	}
	orgID, err := resolveReportOrg(cmd.Context(), f.orgID)
	if err != nil {
		return err
	}
	input, err := readAdminInput(cmd.Context(), f.input, f.data, cmd.InOrStdin())
	if err != nil {
		return err
	}
	if operation.InputRequired && len(input) == 0 {
		return errors.New("this operation requires a JSON object via --input or --data")
	}
	mayWrite := operation.Method != http.MethodGet && operation.Method != http.MethodHead
	if operation.Protocol == adminGraphQL {
		mayWrite = graphqlMayWrite(operation.Document)
	}
	if mayWrite && !f.yes && !f.dryRun {
		return mutationError(cmd, operation.Name, true, errors.New("refusing to change the organization without --yes (use --dry-run to preview)"))
	}

	client := newReportsClient(cmd.Context(), orgID)
	headers := make(http.Header)
	path := operation.Path
	variables := cloneAdminInput(operation.Defaults)
	for key, value := range input {
		variables[key] = value
	}
	variables["orgId"] = orgID
	for index, name := range operation.ArgumentNames {
		variables[name] = args[index]
		path = strings.ReplaceAll(path, "{"+name+"}", url.PathEscape(args[index]))
	}
	path = strings.ReplaceAll(path, "{org}", url.PathEscape(orgID))

	if operation.AutoETag {
		etag := strings.TrimSpace(f.etag)
		if etag == "" || strings.EqualFold(etag, "auto") {
			if !f.dryRun {
				etag, err = loadOrganizationETag(cmd, client, orgID)
				if err != nil {
					return fmt.Errorf("load organization ETag: %w", err)
				}
			} else {
				etag = "<loaded automatically at execution>"
			}
		}
		headers.Set("If-Match", etag)
	}

	var body []byte
	service := operation.Service
	method := operation.Method
	if operation.Protocol == adminGraphQL {
		if service == "" {
			service = orgreports.APIServiceApp
		}
		method = http.MethodPost
		path, err = graphQLEndpointPath(service)
		if err != nil {
			return err
		}
		body, err = json.Marshal(map[string]any{"query": operation.Document, "variables": variables})
	} else {
		query := append([]string{}, operation.DefaultQuery...)
		query = append(query, f.query...)
		path, err = appendAPIQuery(path, query)
		if len(input) > 0 {
			body, err = json.Marshal(input)
		}
	}
	if err != nil {
		return err
	}
	endpoint, err := client.RawEndpoint(service, path)
	if err != nil {
		return err
	}
	preview := apiPreview(method, endpoint, service, body)
	preview["adminArea"] = operation.Area
	preview["adminOperation"] = operation.Name
	if len(headers) > 0 {
		preview["headers"] = headers
	}
	if f.dryRun {
		return writeJSON(cmd.OutOrStdout(), mutationEnvelope{SchemaVersion: "1", Status: "preview", Operation: operation.Name, Organization: orgID, DryRun: true, Request: preview})
	}

	response, err := client.RawRequestDetailed(cmd.Context(), service, method, path, body, headers)
	if err != nil {
		return fmt.Errorf("%s: %w", operation.Name, err)
	}
	if operation.Protocol == adminGraphQL {
		if graphQLError := graphqlResponseError(response.Body); graphQLError != nil {
			return graphQLError
		}
	}
	return writeAdminResponse(cmd, f.out, operation, orgID, response)
}

func readAdminInput(ctx context.Context, filename, inline string, stdin io.Reader) (map[string]any, error) {
	if filename == "" && inline == "" {
		return map[string]any{}, nil
	}
	var data []byte
	var err error
	if filename != "" {
		data, err = readLimitedInput(ctx, filename, stdin, 16<<20)
	} else {
		data = []byte(inline)
	}
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var input map[string]any
	if err := decoder.Decode(&input); err != nil {
		return nil, fmt.Errorf("admin input must be a JSON object: %w", err)
	}
	if input == nil {
		input = map[string]any{}
	}
	return input, nil
}

func cloneAdminInput(input map[string]any) map[string]any {
	copy := make(map[string]any, len(input)+1)
	for key, value := range input {
		copy[key] = value
	}
	return copy
}

func loadOrganizationETag(cmd *op.Call, client *orgreports.Client, orgID string) (string, error) {
	response, err := client.RawRequestDetailed(cmd.Context(), orgreports.APIServiceCore, http.MethodGet, "/v3/orgs/"+url.PathEscape(orgID), nil, nil)
	if err != nil {
		return "", err
	}
	etag := response.Header.Get("ETag")
	if etag == "" {
		return "", errors.New("organization response did not include an ETag")
	}
	return etag, nil
}

func writeAdminResponse(cmd *op.Call, filename string, operation adminOperation, orgID string, response *orgreports.RawResponse) error {
	var result any
	trimmed := bytes.TrimSpace(response.Body)
	if len(trimmed) > 0 {
		if err := json.Unmarshal(trimmed, &result); err != nil {
			result = string(response.Body)
		}
	}
	envelope := map[string]any{
		"schemaVersion": "1", "status": "success", "organization": orgID,
		"area": operation.Area, "operation": operation.Name,
		"statusCode": response.StatusCode, "result": result,
	}
	if etag := response.Header.Get("ETag"); etag != "" {
		envelope["etag"] = etag
	}
	if filename == "" {
		return writeJSON(cmd.OutOrStdout(), envelope)
	}
	file, err := op.RuntimeFrom(cmd.Context()).Files.Create(filename)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	return writeJSON(file, envelope)
}

func adminAreaDescription(area string) string {
	descriptions := map[string]string{
		"organization": "Organization settings", "subsidiaries": "Organization structure and subsidiaries",
		"accounting-setup": "Organization accounting defaults", "billing": "Usage, credits, and billing settings",
		"connections": "Accounting connections and provider configuration", "system-jobs": "Background jobs and bulk actions",
		"wallets": "Administrative wallet operations", "users": "Organization users and invitations",
		"roles": "Roles and permissions", "sso": "SAML single sign-on", "scim": "SCIM provisioning",
		"audit-config": "Audit event delivery configuration", "api-keys": "Organization API credentials",
		"custom-labels": "Organization custom labels", "sftp": "SFTP connections and paths",
		"rolled-up-je": "Rolled-up journal-entry configurations",
	}
	if description := descriptions[area]; description != "" {
		return description
	}
	return "Bitwave Admin operations"
}

func sortedAdminAreas() []string {
	seen := map[string]bool{}
	for _, operation := range adminOperations() {
		seen[operation.Area] = true
	}
	areas := make([]string, 0, len(seen))
	for area := range seen {
		areas = append(areas, area)
	}
	sort.Strings(areas)
	return areas
}
