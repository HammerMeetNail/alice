package cli

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// Env names used by the CLI.
const (
	EnvServerURL   = "ALICE_SERVER_URL"
	EnvServerCA    = "ALICE_SERVER_TLS_CA"
	EnvStateFile   = "ALICE_STATE_FILE"
	EnvAccessToken = "ALICE_ACCESS_TOKEN"
)

// GlobalOptions hold flags shared by every subcommand.
type GlobalOptions struct {
	ServerURL string
	StateFile string
	TLSCAFile string
	Format    OutputFormat
}

// Run dispatches a single CLI invocation. argv is the slice *after* the
// binary name (i.e. os.Args[1:]). stdin/stdout/stderr are the streams the
// subcommands should use; passing non-stdout streams lets tests and hooks
// capture output.
func Run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(argv) == 0 {
		printUsage(stderr)
		return 2
	}
	if argv[0] == "-h" || argv[0] == "--help" || argv[0] == "help" {
		printUsage(stdout)
		return 0
	}

	globalFlags := flag.NewFlagSet("alice", flag.ContinueOnError)
	globalFlags.SetOutput(stderr)

	var (
		serverURL string
		stateFile string
		tlsCAFile string
		jsonOut   bool
	)
	globalFlags.StringVar(&serverURL, "server", os.Getenv(EnvServerURL), "coordination server URL (default: $ALICE_SERVER_URL)")
	globalFlags.StringVar(&stateFile, "state", os.Getenv(EnvStateFile), "path to CLI state file (default: ~/.alice/state.json or $ALICE_STATE_FILE)")
	globalFlags.StringVar(&tlsCAFile, "tls-ca", os.Getenv(EnvServerCA), "path to TLS CA PEM bundle for self-signed servers")
	globalFlags.BoolVar(&jsonOut, "json", false, "emit JSON output instead of human-readable text")

	// Split argv into global flags and the subcommand. Stop at the first
	// non-flag so subcommand-specific flags reach the subcommand parser.
	subArgs, subcommand, err := splitGlobalArgs(argv, globalFlags)
	if err != nil {
		fmt.Fprintln(stderr, "error:", err)
		return 2
	}
	if subcommand == "" {
		printUsage(stderr)
		return 2
	}

	if stateFile == "" {
		path, err := DefaultStatePath()
		if err != nil {
			fmt.Fprintln(stderr, "error:", err)
			return 2
		}
		stateFile = path
	}

	format := FormatText
	if jsonOut {
		format = FormatJSON
	}
	renderer := NewRenderer(format, stdout, stderr)

	opts := GlobalOptions{
		ServerURL: strings.TrimSpace(serverURL),
		StateFile: stateFile,
		TLSCAFile: strings.TrimSpace(tlsCAFile),
		Format:    format,
	}

	handler, ok := subcommands[subcommand]
	if !ok {
		renderer.Errorf("unknown subcommand %q; run `alice help` for a list", subcommand)
		return 2
	}

	if err := handler(ctx, opts, subArgs, stdin, renderer); err != nil {
		renderer.Errorf("%s", err.Error())
		return 1
	}
	return 0
}

// splitGlobalArgs separates leading global-flag arguments from the
// subcommand and its own arguments.
func splitGlobalArgs(argv []string, fs *flag.FlagSet) ([]string, string, error) {
	var globalArgs []string
	remaining := argv
	for len(remaining) > 0 {
		arg := remaining[0]
		if !strings.HasPrefix(arg, "-") {
			break
		}
		if !isGlobalFlag(arg, fs) {
			break
		}
		globalArgs = append(globalArgs, arg)
		remaining = remaining[1:]
		if len(globalArgs) > 0 {
			last := globalArgs[len(globalArgs)-1]
			if !strings.Contains(last, "=") && !isBooleanFlag(last, fs) && len(remaining) > 0 {
				globalArgs = append(globalArgs, remaining[0])
				remaining = remaining[1:]
			}
		}
	}
	if err := fs.Parse(globalArgs); err != nil {
		return nil, "", err
	}
	if len(remaining) == 0 {
		return nil, "", nil
	}
	return remaining[1:], remaining[0], nil
}

func isGlobalFlag(arg string, fs *flag.FlagSet) bool {
	name := strings.TrimLeft(arg, "-")
	if idx := strings.Index(name, "="); idx >= 0 {
		name = name[:idx]
	}
	return fs.Lookup(name) != nil
}

func isBooleanFlag(arg string, fs *flag.FlagSet) bool {
	name := strings.TrimLeft(arg, "-")
	if strings.Contains(name, "=") {
		return true
	}
	f := fs.Lookup(name)
	if f == nil {
		return false
	}
	if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok {
		return bf.IsBoolFlag()
	}
	return false
}

type subcommandFunc func(ctx context.Context, opts GlobalOptions, args []string, stdin io.Reader, r *Renderer) error

var subcommands = map[string]subcommandFunc{
	"init":       cmdInit,
	"register":   cmdRegister,
	"whoami":     cmdWhoami,
	"publish":    cmdPublish,
	"query":      cmdQuery,
	"result":     cmdResult,
	"grant":      cmdGrant,
	"revoke":     cmdRevoke,
	"peers":      cmdPeers,
	"request":    cmdSendRequest,
	"inbox":      cmdInbox,
	"outbox":     cmdOutbox,
	"respond":    cmdRespond,
	"approvals":  cmdListApprovals,
	"approve":    cmdResolveApproval("approve"),
	"deny":       cmdResolveApproval("deny"),
	"audit":      cmdAudit,
	"logout":     cmdLogout,
	"completion": cmdCompletion,
	"tuning":     cmdTuning,
	"policy":     cmdPolicy,
	"actions":    cmdActions,
	"operator":   cmdOperator,
	"team":       cmdTeam,
	"manager":    cmdManager,
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `alice — decentralized coordination for personal AI agents

USAGE
  alice [--server URL] [--json] <subcommand> [flags]

ONBOARDING
  init                    interactive first-run: keypair, register, save session
  register                non-interactive registration
  whoami                  show the current session
  logout                  drop the saved session

PUBLISHING
  publish                 publish a status / blocker / commitment / summary artifact

COORDINATION
  query                   ask a teammate's agent a permission-checked question
  result <query_id>       fetch the result of a prior query
  request                 send a request that may defer to a teammate's human
  respond <request_id>    respond to an incoming request (accept/decline/defer)
  inbox                   list incoming requests
  outbox                  list requests you have sent
  peers                   list peers who can reach you via an active grant

PERMISSIONS
  grant                   grant another user permission to query you
  revoke <grant_id>       revoke a previously issued grant
  approvals               list pending high-risk approvals
  approve <approval_id>   approve a pending request
  deny <approval_id>      deny a pending request

OBSERVABILITY
  audit                   stream recent audit events

ADMIN
  tuning                  set per-org gatekeeper confidence / lookback overrides
  policy apply            apply a new risk policy (admin)
  policy history          list the org's risk policy versions
  policy activate         roll back (or forward) to a saved policy version
  operator enable|disable toggle the operator-phase opt-in for your account
  actions list            list your operator-phase actions
  actions create          create a new action (e.g. acknowledge_blocker)
  actions approve <id>    approve a pending action
  actions cancel <id>     cancel an action you own
  actions execute <id>    execute an approved action
  team create             create an org team (admin)
  team list               list teams in your org
  team delete <team_id>   delete a team (admin)
  team add-member         add a user to a team by email (admin)
  team remove-member      remove a user from a team by email (admin)
  team members <team_id>  list a team's members
  manager set             set user's manager by email (admin)
  manager revoke          clear user's active manager edge (admin)
  manager chain           show a user's upward manager chain

GLOBAL FLAGS
  --server URL            coordination server URL (or set ALICE_SERVER_URL)
  --state PATH            state file path (or set ALICE_STATE_FILE)
  --tls-ca PATH           TLS CA bundle for self-signed servers
  --json                  emit machine-parseable JSON

Run ` + "`alice <subcommand> --help`" + ` for subcommand-specific flags.
`)
}

// loadClient constructs a Client for authenticated subcommands. It requires
// both a saved session and a resolvable server URL.
func loadClient(opts GlobalOptions) (*Client, State, error) {
	state, err := LoadState(opts.StateFile)
	if err != nil {
		return nil, State{}, err
	}

	baseURL := opts.ServerURL
	if baseURL == "" {
		baseURL = state.ServerURL
	}
	if baseURL == "" {
		return nil, state, errors.New("no server URL configured; run `alice init` or pass --server")
	}

	token := state.AccessToken
	if override := strings.TrimSpace(os.Getenv(EnvAccessToken)); override != "" {
		token = override
	}

	client, err := NewClient(ClientOptions{
		BaseURL:     baseURL,
		AccessToken: token,
		TLSCAFile:   opts.TLSCAFile,
	})
	if err != nil {
		return nil, state, err
	}
	return client, state, nil
}

func mustHaveSession(state State) error {
	if !state.HasSession() && strings.TrimSpace(os.Getenv(EnvAccessToken)) == "" {
		return errors.New("not authenticated; run `alice init` first")
	}
	return nil
}

// ---- init / register / whoami / logout ----

func cmdInit(ctx context.Context, opts GlobalOptions, args []string, stdin io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	var (
		serverURL   = fs.String("server", opts.ServerURL, "coordination server URL")
		orgSlug     = fs.String("org", "", "organization slug")
		email       = fs.String("email", "", "owner email")
		agentName   = fs.String("agent", "", "human-readable agent name")
		clientType  = fs.String("client", "cli", "client type identifier")
		inviteToken = fs.String("invite-token", "", "invite token (if your org requires one)")
		force       = fs.Bool("force", false, "overwrite an existing session without prompting")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	existing, _ := LoadState(opts.StateFile)
	if existing.HasSession() && !*force {
		return fmt.Errorf("a session already exists for %s at %s; pass --force to overwrite or run `alice logout` first",
			existing.OwnerEmail, opts.StateFile)
	}

	reader := bufio.NewReader(stdin)
	prompt := func(label, fallback string) (string, error) {
		if fallback != "" {
			return fallback, nil
		}
		if r.Format() == FormatJSON {
			return "", fmt.Errorf("--%s is required in JSON mode", label)
		}
		fmt.Fprintf(r.stdout, "%s: ", label)
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		return strings.TrimSpace(line), nil
	}

	var err error
	*serverURL, err = prompt("server URL", *serverURL)
	if err != nil {
		return err
	}
	if *serverURL == "" {
		return errors.New("server URL is required")
	}
	*orgSlug, err = prompt("org slug", *orgSlug)
	if err != nil {
		return err
	}
	*email, err = prompt("owner email", *email)
	if err != nil {
		return err
	}
	*agentName, err = prompt("agent name", *agentName)
	if err != nil {
		return err
	}
	if *orgSlug == "" || *email == "" || *agentName == "" {
		return errors.New("org, email, and agent name are all required")
	}

	return doRegister(ctx, opts, r, *serverURL, *orgSlug, *email, *agentName, *clientType, *inviteToken)
}

func cmdRegister(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("register", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	var (
		serverURL   = fs.String("server", opts.ServerURL, "coordination server URL")
		orgSlug     = fs.String("org", "", "organization slug")
		email       = fs.String("email", "", "owner email")
		agentName   = fs.String("agent", "", "human-readable agent name")
		clientType  = fs.String("client", "cli", "client type identifier")
		inviteToken = fs.String("invite-token", "", "invite token (if your org requires one)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *serverURL == "" || *orgSlug == "" || *email == "" || *agentName == "" {
		return errors.New("--server, --org, --email, and --agent are required")
	}
	return doRegister(ctx, opts, r, *serverURL, *orgSlug, *email, *agentName, *clientType, *inviteToken)
}

func doRegister(ctx context.Context, opts GlobalOptions, r *Renderer,
	serverURL, orgSlug, email, agentName, clientType, inviteToken string) error {

	client, err := NewClient(ClientOptions{BaseURL: serverURL, TLSCAFile: opts.TLSCAFile})
	if err != nil {
		return err
	}

	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return fmt.Errorf("generate keypair: %w", err)
	}
	publicKeyB64 := base64.StdEncoding.EncodeToString(pub)
	privateKeyB64 := base64.StdEncoding.EncodeToString(priv)

	challengeBody := map[string]any{
		"org_slug":     orgSlug,
		"owner_email":  email,
		"agent_name":   agentName,
		"client_type":  clientType,
		"public_key":   publicKeyB64,
		"invite_token": inviteToken,
	}
	challenge, err := client.Do(ctx, http.MethodPost, "/v1/agents/register/challenge", challengeBody, true)
	if err != nil {
		return fmt.Errorf("request challenge: %w", err)
	}
	challengeID, _ := challenge["challenge_id"].(string)
	challengeStr, _ := challenge["challenge"].(string)
	if challengeID == "" || challengeStr == "" {
		return errors.New("server returned an incomplete challenge payload")
	}

	signature := ed25519.Sign(priv, []byte(challengeStr))
	completeBody := map[string]any{
		"challenge_id":        challengeID,
		"challenge_signature": base64.StdEncoding.EncodeToString(signature),
	}
	response, err := client.Do(ctx, http.MethodPost, "/v1/agents/register", completeBody, true)
	if err != nil {
		return fmt.Errorf("complete registration: %w", err)
	}

	accessToken, _ := response["access_token"].(string)
	agentID, _ := response["agent_id"].(string)
	orgID, _ := response["org_id"].(string)
	expiresRaw, _ := response["access_token_expires_at"].(string)

	var expiresAt time.Time
	if expiresRaw != "" {
		if t, perr := time.Parse(time.RFC3339, expiresRaw); perr == nil {
			expiresAt = t
		}
	}

	state := State{
		ServerURL:      client.BaseURL(),
		OrgSlug:        orgSlug,
		OrgID:          orgID,
		OwnerEmail:     email,
		AgentName:      agentName,
		AgentID:        agentID,
		PublicKey:      publicKeyB64,
		PrivateKey:     privateKeyB64,
		AccessToken:    accessToken,
		TokenExpiresAt: expiresAt,
	}
	if err := SaveState(opts.StateFile, state); err != nil {
		return err
	}

	firstInviteToken := stringFrom(response, "first_invite_token")
	summary := fmt.Sprintf("Registered %s as %s (%s).", email, agentName, agentID)
	if firstInviteToken != "" {
		summary += "\n\nNOTE: This org did not yet have an invite token. One was generated and will be shown once:\n  " +
			firstInviteToken + "\n\nShare it with teammates who will register next. It is not persisted; re-running register will not show it again."
	}
	if status := stringFrom(response, "agent_status"); status != "" && status != "active" {
		summary += fmt.Sprintf("\nStatus: %s — additional verification required before you can query peers.", status)
	}

	payload := map[string]any{
		"agent_id":     agentID,
		"org_id":       orgID,
		"state_file":   opts.StateFile,
		"server_url":   client.BaseURL(),
		"expires_at":   expiresRaw,
		"agent_status": stringFrom(response, "agent_status"),
	}
	if firstInviteToken != "" {
		payload["first_invite_token"] = firstInviteToken
	}
	return r.Emit(summary, payload, false)
}

func cmdWhoami(_ context.Context, opts GlobalOptions, _ []string, _ io.Reader, r *Renderer) error {
	state, err := LoadState(opts.StateFile)
	if err != nil {
		return err
	}
	fields := map[string]any{
		"server_url":  state.ServerURL,
		"org_slug":    state.OrgSlug,
		"owner_email": state.OwnerEmail,
		"agent_name":  state.AgentName,
		"agent_id":    state.AgentID,
		"state_file":  opts.StateFile,
	}
	if !state.TokenExpiresAt.IsZero() {
		fields["token_expires_at"] = state.TokenExpiresAt.Format(time.RFC3339)
		if time.Until(state.TokenExpiresAt) <= 0 {
			fields["token_expired"] = true
		}
	}
	summary := "Current session:"
	if !state.HasSession() {
		summary = "No active session. Run `alice init`."
	}
	return r.Emit(summary, fields, false)
}

func cmdLogout(_ context.Context, opts GlobalOptions, _ []string, _ io.Reader, r *Renderer) error {
	if err := os.Remove(opts.StateFile); err != nil && !os.IsNotExist(err) {
		return err
	}
	return r.Emit("Session cleared.", map[string]any{"state_file": opts.StateFile}, false)
}

// ---- publish ----

func cmdPublish(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("publish", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	var (
		artifactType   = fs.String("type", "summary", "artifact type (summary, status_delta, blocker, commitment, ...)")
		title          = fs.String("title", "", "artifact title")
		content        = fs.String("content", "", "artifact content (use @path to read from a file, or - for stdin)")
		sensitivity    = fs.String("sensitivity", "low", "sensitivity (low, medium, high)")
		visibility     = fs.String("visibility", "explicit_grants_only", "visibility (private, explicit_grants_only, team_scope, manager_scope)")
		confidence     = fs.Float64("confidence", 0.9, "confidence (0.0–1.0)")
		project        = fs.String("project", "", "optional project scope reference")
		supersedes     = fs.String("supersedes", "", "artifact_id of a prior artifact this one replaces")
		expiresIn      = fs.String("expires-in", "", "duration until the artifact expires (e.g. 24h)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *title == "" || *content == "" {
		return errors.New("--title and --content are required")
	}

	body, err := resolveInlineValue(*content)
	if err != nil {
		return fmt.Errorf("resolve --content: %w", err)
	}

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	artifact := map[string]any{
		"type":            *artifactType,
		"title":           *title,
		"content":         body,
		"sensitivity":     *sensitivity,
		"visibility_mode": *visibility,
		"confidence":      *confidence,
		"source_refs": []map[string]any{
			{
				"source_system": "alice_cli",
				"source_type":   "manual",
				"source_id":     randomSourceID(),
				"observed_at":   time.Now().UTC().Format(time.RFC3339),
				"trust_class":   "structured_system",
				"sensitivity":   *sensitivity,
			},
		},
	}
	if *project != "" {
		artifact["structured_payload"] = map[string]any{"project": *project}
	}
	if *supersedes != "" {
		artifact["supersedes_artifact_id"] = *supersedes
	}
	if *expiresIn != "" {
		dur, err := time.ParseDuration(*expiresIn)
		if err != nil {
			return fmt.Errorf("parse --expires-in: %w", err)
		}
		artifact["expires_at"] = time.Now().UTC().Add(dur).Format(time.RFC3339)
	}
	resp, err := client.Do(ctx, http.MethodPost, "/v1/artifacts", map[string]any{"artifact": artifact}, false)
	if err != nil {
		return err
	}
	artifactID := stringFrom(resp, "artifact_id")
	summary := fmt.Sprintf("Published artifact %s.", artifactID)
	return r.Emit(summary, resp, false)
}

// ---- query / result ----

func cmdQuery(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("query", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	var (
		to       = fs.String("to", "", "recipient user email")
		purpose  = fs.String("purpose", "status_check", "query purpose (status_check, dependency_check, handoff, manager_update, request_context)")
		question = fs.String("question", "", "natural-language question")
		types    = fs.String("types", "summary,status_delta,blocker,commitment", "comma-separated requested artifact types")
		project  = fs.String("project", "", "optional project scope")
		since    = fs.String("since", "", "time window start (RFC3339 or duration like 24h)")
		until    = fs.String("until", "", "time window end (RFC3339, default now)")
		wait     = fs.Bool("wait", true, "wait for the result; pass --wait=false to return only the query id")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" || *question == "" {
		return errors.New("--to and --question are required")
	}

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	window, err := resolveTimeWindow(*since, *until)
	if err != nil {
		return err
	}

	body := map[string]any{
		"to_user_email":   *to,
		"purpose":         *purpose,
		"question":        *question,
		"requested_types": splitCSV(*types),
		"time_window":     window,
	}
	if *project != "" {
		body["project_scope"] = splitCSV(*project)
	}

	resp, err := client.Do(ctx, http.MethodPost, "/v1/queries", body, false)
	if err != nil {
		return err
	}
	queryID := stringFrom(resp, "query_id")
	if !*wait {
		return r.Emit(fmt.Sprintf("Query %s submitted.", queryID), resp, false)
	}
	result, err := pollQueryResult(ctx, client, queryID, 15*time.Second)
	if err != nil {
		return err
	}
	return renderQueryResult(r, result)
}

func cmdResult(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	if len(args) == 0 {
		return errors.New("usage: alice result <query_id>")
	}
	queryID := args[0]
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}
	result, err := client.Do(ctx, http.MethodGet, "/v1/queries/"+url.PathEscape(queryID), nil, false)
	if err != nil {
		return err
	}
	return renderQueryResult(r, result)
}

func pollQueryResult(ctx context.Context, client *Client, queryID string, timeout time.Duration) (map[string]any, error) {
	deadline := time.Now().Add(timeout)
	backoff := 200 * time.Millisecond
	for {
		result, err := client.Do(ctx, http.MethodGet, "/v1/queries/"+url.PathEscape(queryID), nil, false)
		if err != nil {
			return nil, err
		}
		if state := stringFrom(result, "state"); state == "completed" || state == "denied" || state == "expired" {
			return result, nil
		}
		if time.Now().After(deadline) {
			return result, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
		}
		if backoff < 2*time.Second {
			backoff *= 2
		}
	}
}

func renderQueryResult(r *Renderer, payload map[string]any) error {
	state := stringFrom(payload, "state")
	summary := fmt.Sprintf("Query state: %s", state)

	// Surface provenance and confidence up front so humans and agents see them
	// before touching the untrusted content block.
	response, _ := payload["response"].(map[string]any)
	if response != nil {
		if conf, ok := response["confidence"].(float64); ok {
			summary += fmt.Sprintf("  confidence=%.2f", conf)
		}
		if basis, ok := response["policy_basis"].([]any); ok && len(basis) > 0 {
			pieces := make([]string, 0, len(basis))
			for _, b := range basis {
				if s, ok := b.(string); ok {
					pieces = append(pieces, s)
				}
			}
			if len(pieces) > 0 {
				summary += "  policy=" + strings.Join(pieces, ",")
			}
		}
	}
	return r.Emit(summary, payload, true)
}

// ---- grant / revoke / peers ----

func cmdGrant(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("grant", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	var (
		to             = fs.String("to", "", "grantee user email")
		scopeType      = fs.String("scope-type", "project", "scope type (user, team, org, project)")
		scopeRef       = fs.String("scope-ref", "*", "scope reference (use * to match any project; a specific name to filter)")
		types          = fs.String("types", "summary,status_delta", "comma-separated allowed artifact types")
		maxSensitivity = fs.String("sensitivity", "medium", "maximum allowed sensitivity")
		purposes       = fs.String("purposes", "status_check,request_context", "comma-separated allowed purposes (status_check, dependency_check, handoff, manager_update, request_context)")
		expiresIn      = fs.String("expires-in", "", "duration until grant expires (e.g. 720h)")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" {
		return errors.New("--to is required")
	}
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	body := map[string]any{
		"grantee_user_email":     *to,
		"scope_type":             *scopeType,
		"scope_ref":              *scopeRef,
		"allowed_artifact_types": splitCSV(*types),
		"max_sensitivity":        *maxSensitivity,
		"allowed_purposes":       splitCSV(*purposes),
	}
	if *expiresIn != "" {
		dur, err := time.ParseDuration(*expiresIn)
		if err != nil {
			return fmt.Errorf("parse --expires-in: %w", err)
		}
		body["expires_at"] = time.Now().UTC().Add(dur).Format(time.RFC3339)
	}
	resp, err := client.Do(ctx, http.MethodPost, "/v1/policy-grants", body, false)
	if err != nil {
		return err
	}
	return r.Emit(fmt.Sprintf("Granted %s access to %s.", *to, strings.Join(splitCSV(*types), ",")), resp, false)
}

func cmdRevoke(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	if len(args) == 0 {
		return errors.New("usage: alice revoke <policy_grant_id>")
	}
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}
	resp, err := client.Do(ctx, http.MethodDelete, "/v1/policy-grants/"+url.PathEscape(args[0]), nil, false)
	if err != nil {
		return err
	}
	return r.Emit(fmt.Sprintf("Revoked grant %s.", args[0]), resp, false)
}

func cmdPeers(ctx context.Context, opts GlobalOptions, _ []string, _ io.Reader, r *Renderer) error {
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}
	resp, err := client.Do(ctx, http.MethodGet, "/v1/peers", nil, false)
	if err != nil {
		return err
	}
	items := ExtractList(resp, "peers", "items")
	return r.EmitList("Peers with active grants:", items, false)
}

// ---- requests ----

func cmdSendRequest(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("request", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	var (
		to          = fs.String("to", "", "recipient user email")
		requestType = fs.String("type", "question", "request type (question, ask_for_time, review, ...)")
		title       = fs.String("title", "", "request title")
		content     = fs.String("content", "", "request content / body")
		expiresIn   = fs.String("expires-in", "24h", "duration until the request expires")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == "" || *title == "" || *content == "" {
		return errors.New("--to, --title, and --content are required")
	}
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	payload := map[string]any{
		"to_user_email": *to,
		"request_type":  *requestType,
		"title":         *title,
		"content":       *content,
	}
	if *expiresIn != "" {
		dur, err := time.ParseDuration(*expiresIn)
		if err != nil {
			return fmt.Errorf("parse --expires-in: %w", err)
		}
		payload["expires_at"] = time.Now().UTC().Add(dur).Format(time.RFC3339)
	}
	resp, err := client.Do(ctx, http.MethodPost, "/v1/requests", payload, false)
	if err != nil {
		return err
	}
	return r.Emit(fmt.Sprintf("Request %s sent.", stringFrom(resp, "request_id")), resp, false)
}

func cmdInbox(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("inbox", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	watch := fs.Bool("watch", false, "poll continuously and surface newly-arrived requests")
	interval := fs.Duration("interval", 5*time.Second, "poll interval when --watch is set (minimum 1s)")
	limit := fs.Int("limit", 0, "maximum number of results per poll")
	cursor := fs.String("cursor", "", "pagination cursor (ignored in --watch mode)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*watch {
		return fetchAndRenderRequests(ctx, opts, r, "/v1/requests/incoming", "Incoming requests:",
			*limit, *cursor)
	}
	if *interval < time.Second {
		*interval = time.Second
	}
	return watchInbox(ctx, opts, r, *interval, *limit)
}

func cmdOutbox(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	return listRequests(ctx, opts, args, r, "/v1/requests/sent", "Sent requests:")
}

// watchInbox polls /v1/requests/incoming on the given interval and emits
// newly-appeared requests as they arrive. First poll prints the current
// pending set; subsequent polls only surface request_ids not seen before.
func watchInbox(ctx context.Context, opts GlobalOptions, r *Renderer, interval time.Duration, limit int) error {
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	query := ""
	if limit > 0 {
		query = "?limit=" + strconv.Itoa(limit)
	}

	seen := map[string]struct{}{}
	first := true

	poll := func() error {
		resp, err := client.Do(ctx, http.MethodGet, "/v1/requests/incoming"+query, nil, false)
		if err != nil {
			return err
		}
		items := ExtractList(resp, "requests", "items")
		fresh := make([]map[string]any, 0, len(items))
		for _, item := range items {
			id, _ := item["request_id"].(string)
			if id == "" {
				continue
			}
			if _, ok := seen[id]; ok {
				continue
			}
			seen[id] = struct{}{}
			fresh = append(fresh, item)
		}
		if first {
			first = false
			return r.EmitList("Incoming requests (watching — press Ctrl-C to stop):", fresh, true)
		}
		if len(fresh) == 0 {
			return nil
		}
		header := fmt.Sprintf("[%s] %d new incoming request(s):",
			time.Now().UTC().Format(time.RFC3339), len(fresh))
		return r.EmitList(header, fresh, true)
	}

	if err := poll(); err != nil {
		return err
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			if err := poll(); err != nil {
				r.Errorf("watch poll failed: %v", err)
			}
		}
	}
}

// fetchAndRenderRequests is the flag-free core of listRequests. cmdInbox parses
// its own flags (to add --watch) and calls this directly; other callers use
// listRequests, which wraps this with its own flag set.
func fetchAndRenderRequests(ctx context.Context, opts GlobalOptions, r *Renderer, path, title string, limit int, cursor string) error {
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}
	query := ""
	if limit > 0 {
		query = "?limit=" + strconv.Itoa(limit)
	}
	if cursor != "" {
		if query == "" {
			query = "?"
		} else {
			query += "&"
		}
		query += "cursor=" + url.QueryEscape(cursor)
	}
	resp, err := client.Do(ctx, http.MethodGet, path+query, nil, false)
	if err != nil {
		return err
	}
	items := ExtractList(resp, "requests", "items")
	return r.EmitList(title, items, true)
}

func listRequests(ctx context.Context, opts GlobalOptions, args []string, r *Renderer, path, title string) error {
	fs := flag.NewFlagSet("requests", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	limit := fs.Int("limit", 0, "maximum number of results")
	cursor := fs.String("cursor", "", "pagination cursor")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return fetchAndRenderRequests(ctx, opts, r, path, title, *limit, *cursor)
}

func cmdRespond(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	if len(args) == 0 {
		return errors.New("usage: alice respond <request_id> [--response accept|decline|defer] [--message \"...\"]")
	}
	requestID := args[0]
	fs := flag.NewFlagSet("respond", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	response := fs.String("response", "accept", "response action (accept, decline, defer)")
	message := fs.String("message", "", "optional message to the sender")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}
	resp, err := client.Do(ctx, http.MethodPost, "/v1/requests/"+url.PathEscape(requestID)+"/respond", map[string]any{
		"response": *response,
		"message":  *message,
	}, false)
	if err != nil {
		return err
	}
	return r.Emit(fmt.Sprintf("Responded to %s with %s.", requestID, *response), resp, false)
}

// ---- approvals ----

func cmdListApprovals(ctx context.Context, opts GlobalOptions, _ []string, _ io.Reader, r *Renderer) error {
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}
	resp, err := client.Do(ctx, http.MethodGet, "/v1/approvals", nil, false)
	if err != nil {
		return err
	}
	items := ExtractList(resp, "approvals", "items")
	return r.EmitList("Pending approvals:", items, true)
}

func cmdResolveApproval(decision string) subcommandFunc {
	return func(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
		if len(args) == 0 {
			return fmt.Errorf("usage: alice %s <approval_id>", decision)
		}
		client, state, err := loadClient(opts)
		if err != nil {
			return err
		}
		if err := mustHaveSession(state); err != nil {
			return err
		}
		resp, err := client.Do(ctx, http.MethodPost, "/v1/approvals/"+url.PathEscape(args[0])+"/resolve",
			map[string]any{"decision": decision}, false)
		if err != nil {
			return err
		}
		return r.Emit(fmt.Sprintf("Approval %s %sed.", args[0], decision), resp, false)
	}
}

// ---- audit ----

func cmdAudit(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("audit", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	since := fs.String("since", "", "RFC3339 timestamp or duration (e.g. 24h)")
	eventKind := fs.String("event-kind", "", "filter by event kind")
	limit := fs.Int("limit", 0, "max events")
	if err := fs.Parse(args); err != nil {
		return err
	}
	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}
	query := ""
	if *since != "" {
		ts, err := resolveTimestamp(*since)
		if err != nil {
			return fmt.Errorf("--since: %w", err)
		}
		query = "?since=" + url.QueryEscape(ts.Format(time.RFC3339))
	}
	if *eventKind != "" {
		if query == "" {
			query = "?"
		} else {
			query += "&"
		}
		query += "event_kind=" + url.QueryEscape(*eventKind)
	}
	if *limit > 0 {
		if query == "" {
			query = "?"
		} else {
			query += "&"
		}
		query += "limit=" + strconv.Itoa(*limit)
	}
	resp, err := client.Do(ctx, http.MethodGet, "/v1/audit/summary"+query, nil, false)
	if err != nil {
		return err
	}
	items := ExtractList(resp, "events", "items")
	return r.EmitList("Audit events:", items, false)
}

// ---- shell completion ----

func cmdTuning(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("tuning", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	var (
		confidence = fs.Float64("confidence", -1, "gatekeeper auto-answer confidence threshold (0 < x ≤ 1); omit to leave unchanged")
		lookback   = fs.String("lookback", "", "gatekeeper artifact lookback window (Go duration, e.g. 720h); omit to leave unchanged")
		clear      = fs.Bool("clear", false, "clear both overrides and revert to the server-wide default")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	body := map[string]any{}
	switch {
	case *clear:
		body["clear"] = true
	default:
		if *confidence >= 0 {
			body["confidence_threshold"] = *confidence
		}
		if *lookback != "" {
			// Sanity check at the CLI layer; the server re-validates.
			if _, err := time.ParseDuration(*lookback); err != nil {
				return fmt.Errorf("--lookback must be a Go duration (e.g. 720h): %w", err)
			}
			body["lookback_window"] = *lookback
		}
		if len(body) == 0 {
			return errors.New("pass at least one of --confidence, --lookback, or --clear")
		}
	}

	resp, err := client.Do(ctx, http.MethodPost, "/v1/orgs/gatekeeper-tuning", body, false)
	if err != nil {
		return err
	}
	fields := map[string]any{
		"org_id": stringFrom(resp, "org_id"),
	}
	if v, ok := resp["confidence_threshold"].(float64); ok {
		fields["confidence_threshold"] = v
	} else {
		fields["confidence_threshold"] = "(server default)"
	}
	if v := stringFrom(resp, "lookback_window"); v != "" {
		fields["lookback_window"] = v
	} else {
		fields["lookback_window"] = "(server default)"
	}
	return r.Emit("gatekeeper tuning updated", fields, false)
}

func cmdPolicy(ctx context.Context, opts GlobalOptions, args []string, stdin io.Reader, r *Renderer) error {
	if len(args) == 0 {
		return errors.New("usage: alice policy apply|history|activate [flags]")
	}
	sub := args[0]
	rest := args[1:]
	switch sub {
	case "apply":
		return cmdPolicyApply(ctx, opts, rest, stdin, r)
	case "history":
		return cmdPolicyHistory(ctx, opts, rest, r)
	case "activate":
		return cmdPolicyActivate(ctx, opts, rest, r)
	default:
		return fmt.Errorf("unknown policy subcommand %q (valid: apply, history, activate)", sub)
	}
}

func cmdPolicyApply(ctx context.Context, opts GlobalOptions, args []string, stdin io.Reader, r *Renderer) error {
	fs := flag.NewFlagSet("policy apply", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	var (
		file = fs.String("file", "", "path to a policy JSON file; pass - to read from stdin")
		name = fs.String("name", "", "optional human-readable policy name")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	var raw []byte
	switch {
	case *file == "":
		return errors.New("--file is required (path to policy JSON, or - for stdin)")
	case *file == "-":
		data, err := io.ReadAll(stdin)
		if err != nil {
			return fmt.Errorf("read policy from stdin: %w", err)
		}
		raw = data
	default:
		data, err := os.ReadFile(*file)
		if err != nil {
			return fmt.Errorf("read policy file: %w", err)
		}
		raw = data
	}

	var source any
	if err := json.Unmarshal(raw, &source); err != nil {
		return fmt.Errorf("policy file is not valid JSON: %w", err)
	}

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	resp, err := client.Do(ctx, http.MethodPost, "/v1/orgs/risk-policy", map[string]any{
		"name":   *name,
		"source": source,
	}, false)
	if err != nil {
		return err
	}
	return r.Emit("risk policy applied", map[string]any{
		"policy_id": stringFrom(resp, "policy_id"),
		"version":   resp["version"],
		"name":      stringFrom(resp, "name"),
		"active_at": resp["active_at"],
	}, false)
}

func cmdPolicyHistory(ctx context.Context, opts GlobalOptions, args []string, r *Renderer) error {
	fs := flag.NewFlagSet("policy history", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	limit := fs.Int("limit", 20, "max policies to return")
	if err := fs.Parse(args); err != nil {
		return err
	}

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	resp, err := client.Do(ctx, http.MethodGet, fmt.Sprintf("/v1/orgs/risk-policies?limit=%d", *limit), nil, false)
	if err != nil {
		return err
	}
	items := ExtractList(resp, "policies")
	rendered := make([]map[string]any, 0, len(items))
	for _, item := range items {
		row := map[string]any{
			"policy_id": stringFrom(item, "policy_id"),
			"version":   item["version"],
		}
		if name := stringFrom(item, "name"); name != "" {
			row["name"] = name
		}
		if active, ok := item["active_at"]; ok && active != nil {
			row["active"] = true
		}
		rendered = append(rendered, row)
	}
	return r.EmitList("risk policy history", rendered, false)
}

func cmdPolicyActivate(ctx context.Context, opts GlobalOptions, args []string, r *Renderer) error {
	fs := flag.NewFlagSet("policy activate", flag.ContinueOnError)
	fs.SetOutput(r.stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: alice policy activate <policy_id>")
	}
	policyID := fs.Arg(0)

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	resp, err := client.Do(ctx, http.MethodPost, "/v1/orgs/risk-policies/"+policyID+"/activate", nil, false)
	if err != nil {
		return err
	}
	return r.Emit("risk policy activated", map[string]any{
		"policy_id": stringFrom(resp, "policy_id"),
		"version":   resp["version"],
		"active_at": resp["active_at"],
	}, false)
}

func cmdOperator(ctx context.Context, opts GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	if len(args) == 0 {
		return errors.New("usage: alice operator enable|disable")
	}
	var enabled bool
	switch args[0] {
	case "enable":
		enabled = true
	case "disable":
		enabled = false
	default:
		return fmt.Errorf("unknown operator subcommand %q (valid: enable, disable)", args[0])
	}

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	resp, err := client.Do(ctx, http.MethodPost, "/v1/users/me/operator-enabled", map[string]any{
		"enabled": enabled,
	}, false)
	if err != nil {
		return err
	}
	verb := "disabled"
	if enabled {
		verb = "enabled"
	}
	return r.Emit("operator phase "+verb, map[string]any{
		"user_id":          stringFrom(resp, "user_id"),
		"operator_enabled": resp["operator_enabled"],
	}, false)
}

func cmdActions(ctx context.Context, opts GlobalOptions, args []string, stdin io.Reader, r *Renderer) error {
	if len(args) == 0 {
		return errors.New("usage: alice actions list|create|approve|cancel|execute [flags]")
	}
	sub := args[0]
	rest := args[1:]

	client, state, err := loadClient(opts)
	if err != nil {
		return err
	}
	if err := mustHaveSession(state); err != nil {
		return err
	}

	switch sub {
	case "list":
		fs := flag.NewFlagSet("actions list", flag.ContinueOnError)
		fs.SetOutput(r.stderr)
		stateFilter := fs.String("state", "", "optional state filter (pending|approved|executing|executed|failed|cancelled|expired)")
		limit := fs.Int("limit", 20, "max results")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		q := fmt.Sprintf("?limit=%d", *limit)
		if *stateFilter != "" {
			q += "&state=" + url.QueryEscape(*stateFilter)
		}
		resp, err := client.Do(ctx, http.MethodGet, "/v1/actions"+q, nil, false)
		if err != nil {
			return err
		}
		return r.EmitList("actions", ExtractList(resp, "actions"), false)

	case "create":
		fs := flag.NewFlagSet("actions create", flag.ContinueOnError)
		fs.SetOutput(r.stderr)
		var (
			kind        = fs.String("kind", "", "action kind (e.g. acknowledge_blocker)")
			requestID   = fs.String("request", "", "request id that authorises this action")
			message     = fs.String("message", "", "inline message for acknowledge_blocker; pass @path or - for stdin")
			riskLevel   = fs.String("risk-level", "L1", "risk level (L0..L4)")
			requestType = fs.String("request-type", "", "optional request type for risk-policy evaluation")
		)
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if *kind == "" {
			return errors.New("--kind is required")
		}
		inputs := map[string]any{}
		if *message != "" {
			msg, err := resolveInlineValue(*message)
			if err != nil {
				return fmt.Errorf("--message: %w", err)
			}
			inputs["message"] = msg
		}
		body := map[string]any{
			"kind":         *kind,
			"inputs":       inputs,
			"risk_level":   *riskLevel,
			"request_id":   *requestID,
			"request_type": *requestType,
		}
		resp, err := client.Do(ctx, http.MethodPost, "/v1/actions", body, false)
		if err != nil {
			return err
		}
		return r.Emit("action created", map[string]any{
			"action_id": stringFrom(resp, "action_id"),
			"state":     resp["state"],
			"kind":      resp["kind"],
		}, false)

	case "approve", "cancel", "execute":
		if len(rest) == 0 {
			return fmt.Errorf("usage: alice actions %s <action_id>", sub)
		}
		actionID := rest[0]
		resp, err := client.Do(ctx, http.MethodPost, "/v1/actions/"+actionID+"/"+sub, nil, false)
		if err != nil {
			return err
		}
		return r.Emit("action "+sub, map[string]any{
			"action_id":      stringFrom(resp, "action_id"),
			"state":          resp["state"],
			"failure_reason": stringFrom(resp, "failure_reason"),
		}, false)

	default:
		return fmt.Errorf("unknown actions subcommand %q (valid: list, create, approve, cancel, execute)", sub)
	}
}

func cmdCompletion(_ context.Context, _ GlobalOptions, args []string, _ io.Reader, r *Renderer) error {
	if len(args) == 0 {
		return errors.New("usage: alice completion bash|zsh|fish")
	}
	switch strings.ToLower(args[0]) {
	case "bash":
		_, err := fmt.Fprint(r.stdout, completionBash)
		return err
	case "zsh":
		_, err := fmt.Fprint(r.stdout, completionZsh)
		return err
	case "fish":
		_, err := fmt.Fprint(r.stdout, completionFish)
		return err
	default:
		return fmt.Errorf("unsupported shell %q (expected bash, zsh, or fish)", args[0])
	}
}

// completionSubcommands is the canonical list sourced by every shell script.
// Keep in sync with the subcommands map above; tests assert both lists match.
const completionSubcommands = "init register whoami publish query result grant revoke peers request inbox outbox respond approvals approve deny audit logout completion tuning policy actions operator team manager"

const completionBash = `# alice bash completion. Install by running:
#   alice completion bash > /usr/local/etc/bash_completion.d/alice
# or sourcing at shell startup:
#   source <(alice completion bash)
_alice_complete() {
    local cur prev words cword
    _init_completion || return
    local subcommands="` + completionSubcommands + `"
    if [[ ${cword} -eq 1 ]]; then
        COMPREPLY=( $(compgen -W "${subcommands}" -- "${cur}") )
        return 0
    fi
    # Flag-name completion for the current subcommand happens via the generic
    # long-option prefix; alice does not expose dynamic value completion.
    if [[ "${cur}" == --* ]]; then
        case "${words[1]}" in
            register) COMPREPLY=( $(compgen -W "--server --org --email --agent --invite-token --state --json" -- "${cur}") ) ;;
            publish) COMPREPLY=( $(compgen -W "--type --title --content --sensitivity --visibility --confidence --ttl --state --json" -- "${cur}") ) ;;
            query) COMPREPLY=( $(compgen -W "--to --purpose --question --types --sensitivity --state --json" -- "${cur}") ) ;;
            grant) COMPREPLY=( $(compgen -W "--to --types --sensitivity --purposes --scope-kind --scope-id --expires --state --json" -- "${cur}") ) ;;
            request) COMPREPLY=( $(compgen -W "--to --type --title --content --expires --state --json" -- "${cur}") ) ;;
            inbox) COMPREPLY=( $(compgen -W "--watch --interval --limit --cursor --state --json" -- "${cur}") ) ;;
            respond) COMPREPLY=( $(compgen -W "--response --message --state --json" -- "${cur}") ) ;;
            tuning) COMPREPLY=( $(compgen -W "--confidence --lookback --clear --state --json" -- "${cur}") ) ;;
            policy) COMPREPLY=( $(compgen -W "apply history activate" -- "${cur}") ) ;;
            *) COMPREPLY=( $(compgen -W "--server --state --json" -- "${cur}") ) ;;
        esac
        return 0
    fi
}
complete -F _alice_complete alice
`

const completionZsh = `#compdef alice
# alice zsh completion. Install by running:
#   alice completion zsh > "${fpath[1]}/_alice"
# then restart the shell, or source directly:
#   source <(alice completion zsh)
_alice() {
    local -a subcommands
    subcommands=(` + "`echo \"" + completionSubcommands + "\" | tr ' ' '\\n' | sed 's/^/\"/;s/$/\"/' | paste -sd' ' -`" + `)
    local subs=(init:"start a new session" register:"register with an org" whoami:"print current identity" publish:"publish an artifact" query:"query a peer" result:"fetch query result" grant:"grant a peer access" revoke:"revoke a grant" peers:"list peers with active grants" request:"send a request" inbox:"list incoming requests" outbox:"list sent requests" respond:"respond to a request" approvals:"list pending approvals" approve:"approve a pending approval" deny:"deny a pending approval" audit:"show audit events" logout:"clear local session" completion:"emit shell completion" tuning:"set per-org gatekeeper overrides" policy:"manage org risk policies")
    _arguments -C \
        '1: :->sub' \
        '*:: :->args'
    case $state in
        sub)
            _describe 'subcommand' subs
            ;;
        args)
            case $words[1] in
                completion) _values 'shell' bash zsh fish ;;
                inbox) _values 'flag' --watch --interval --limit --cursor --state --json ;;
                tuning) _values 'flag' --confidence --lookback --clear --state --json ;;
                policy) _values 'policy sub' apply history activate ;;
                *) _values 'flag' --server --state --json ;;
            esac
            ;;
    esac
}
compdef _alice alice
`

const completionFish = `# alice fish completion. Install by running:
#   alice completion fish > ~/.config/fish/completions/alice.fish
set -l subcommands ` + completionSubcommands + `
complete -c alice -f -n '__fish_use_subcommand' -a "$subcommands"
complete -c alice -n '__fish_seen_subcommand_from inbox' -l watch -d 'poll continuously'
complete -c alice -n '__fish_seen_subcommand_from inbox' -l interval -d 'poll interval'
complete -c alice -n '__fish_seen_subcommand_from inbox outbox' -l limit -d 'max results'
complete -c alice -n '__fish_seen_subcommand_from completion' -a 'bash zsh fish'
complete -c alice -n '__fish_seen_subcommand_from tuning' -l confidence -d 'confidence threshold (0,1]'
complete -c alice -n '__fish_seen_subcommand_from tuning' -l lookback -d 'Go duration string'
complete -c alice -n '__fish_seen_subcommand_from tuning' -l clear -d 'revert to server default'
complete -c alice -n '__fish_seen_subcommand_from policy' -a 'apply history activate'
complete -c alice -l server -d 'coordination server URL'
complete -c alice -l state -d 'path to state file'
complete -c alice -l json -d 'emit JSON output'
`

// ---- helpers ----

func randomSourceID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "cli-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	}
	return "cli-" + hex.EncodeToString(buf[:])
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func stringFrom(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return s
	}
	return ""
}

// resolveInlineValue expands a value that may be a literal string, "@path" to
// load from a file, or "-" to read from stdin. Used for --content-style flags
// so artifact bodies can be piped in.
func resolveInlineValue(v string) (string, error) {
	if v == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\n"), nil
	}
	if strings.HasPrefix(v, "@") {
		b, err := os.ReadFile(v[1:])
		if err != nil {
			return "", err
		}
		return strings.TrimRight(string(b), "\n"), nil
	}
	return v, nil
}

func resolveTimestamp(v string) (time.Time, error) {
	if v == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return t, nil
	}
	if dur, err := time.ParseDuration(v); err == nil {
		return time.Now().UTC().Add(-dur), nil
	}
	return time.Time{}, fmt.Errorf("cannot parse %q as RFC3339 timestamp or duration", v)
}

func resolveTimeWindow(since, until string) (map[string]any, error) {
	var start, end time.Time
	var err error
	if since == "" {
		start = time.Now().UTC().Add(-7 * 24 * time.Hour)
	} else {
		start, err = resolveTimestamp(since)
		if err != nil {
			return nil, fmt.Errorf("--since: %w", err)
		}
	}
	if until == "" {
		end = time.Now().UTC()
	} else {
		end, err = resolveTimestamp(until)
		if err != nil {
			return nil, fmt.Errorf("--until: %w", err)
		}
	}
	return map[string]any{
		"start": start.Format(time.RFC3339),
		"end":   end.Format(time.RFC3339),
	}, nil
}
