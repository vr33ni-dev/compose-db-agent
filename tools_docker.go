package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ---------- Tool plumbing (ToolDecl is defined in main.go) ----------

type ToolFunc func(map[string]any) (string, bool, error) // (content, isError, err)

type Tool struct {
	Decl ToolDecl
	Call ToolFunc
}

var tools = map[string]Tool{}

func toolDecls() []ToolDecl {
	out := make([]ToolDecl, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Decl)
	}
	return out
}

func callTool(name string, args map[string]any) (string, bool, error) {
	t, ok := tools[name]
	if !ok {
		return fmt.Sprintf(`{"error":"unknown tool %q"}`, name), true, nil
	}
	return t.Call(args)
}

// ---------- Compose v1/v2 detection & runners ----------

var dryRun = os.Getenv("DRY_RUN") == "1"

// either ["docker","compose"] or ["docker-compose"]
var composeBase []string

func init() {
	composeBase = detectCompose()
	registerTools()
}

func detectCompose() []string {
	// allow explicit override
	switch os.Getenv("COMPOSE_CMD") {
	case "docker-compose":
		return []string{"docker-compose"}
	case "docker compose", "docker":
		return []string{"docker", "compose"}
	}
	// try v2 first
	if checkCmd("docker", "compose", "version") == nil {
		return []string{"docker", "compose"}
	}
	// then legacy v1
	if checkCmd("docker-compose", "version") == nil {
		return []string{"docker-compose"}
	}
	// default to v2 shape
	return []string{"docker", "compose"}
}

func checkCmd(name string, args ...string) error {
	// always probe with a real exec (even if DRY_RUN=1)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func runCompose(args ...string) (string, error) {
	if len(composeBase) == 2 {
		// docker compose …
		return run(composeBase[0], append([]string{composeBase[1]}, args...)...)
	}
	// docker-compose …
	return run(composeBase[0], args...)
}

func runComposeWithEnv(extra map[string]string, args ...string) (string, error) {
	// If APP_DIR is set, act as if we executed from the app repo
	appDir := os.Getenv("APP_DIR")
	if appDir != "" && !contains(args, "--project-directory") {
		args = append([]string{"--project-directory", appDir}, args...)
	}
	if len(composeBase) == 2 {
		return runWithEnv(extra, composeBase[0], append([]string{composeBase[1]}, args...)...)
	}
	return runWithEnv(extra, composeBase[0], args...)
}

func contains(sl []string, x string) bool {
	for _, s := range sl {
		if s == x {
			return true
		}
	}
	return false
}

// ---------- Helpers ----------

func run(name string, args ...string) (string, error) {
	cmdLine := name + " " + strings.Join(args, " ")
	if dryRun {
		return "[dry-run] " + cmdLine, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String() + errb.String(), fmt.Errorf("%s: %w\n%s", cmdLine, err, errb.String())
	}
	return out.String(), nil
}

// Like run, but adds environment variables (for compose var substitution)
func runWithEnv(extra map[string]string, name string, args ...string) (string, error) {
	cmdLine := name + " " + strings.Join(args, " ")
	if dryRun {
		return "[dry-run] " + cmdLine, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = os.Environ()
	for k, v := range extra {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String() + errb.String(), fmt.Errorf("%s: %w\n%s", cmdLine, err, errb.String())
	}
	return out.String(), nil
}

// JSON marshal helper (turn any struct into JSON string)
func j(m any) string { b, _ := json.Marshal(m); return string(b) }

// askYesNo prints a prompt and returns true for yes, false for no.
// If stdin is not a TTY (CI), returns def.
func askYesNo(prompt string, def bool) bool {
	// best-effort: if not interactive, just use default
	if fi, _ := os.Stdin.Stat(); (fi.Mode() & os.ModeCharDevice) == 0 {
		return def
	}
	fmt.Print(prompt)
	rd := bufio.NewReader(os.Stdin)
	s, _ := rd.ReadString('\n')
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return def
	}
	return s == "y" || s == "yes"
}

// allow alnum dot underscore dash
func safeProject(p string) error {
	ok, _ := regexp.MatchString(`^[a-zA-Z0-9._-]+$`, p)
	if !ok {
		return fmt.Errorf("invalid project name: %q", p)
	}
	if os.Getenv("ENV") == "production" {
		return errors.New("refusing to run in production ENV")
	}
	return nil
}

// allow relative paths; if they contain "..", only allow the exact COMPOSE_FILE from env
func safeComposePath(p string) error {
	if strings.Contains(p, "..") {
		allowed := os.Getenv("COMPOSE_FILE")
		if p != allowed {
			return fmt.Errorf("disallowed path: %q (only allowed: %q)", p, allowed)
		}
	}
	return nil
}

// Resolve the container ID for a service (works w/ or w/o container_name)
func containerID(project, composeFile, service string) (string, error) {
	args := []string{"-p", project}
	if composeFile != "" {
		args = append(args, "-f", composeFile)
	}
	args = append(args, "ps", "-q", service)
	out, err := runCompose(args...)
	id := strings.TrimSpace(out)
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("no container for service %q (project %q)", service, project)
	}
	return id, nil
}

// Ensure Docker daemon is reachable; if not, start Colima and wait.
func ensureDockerReady() (string, error) {
	// Already up?
	if _, err := run("docker", "info"); err == nil {
		return "ok", nil
	}

	// Try Colima
	if err := checkCmd("colima", "version"); err != nil {
		return "", fmt.Errorf("docker daemon not reachable and 'colima' not found; start Docker/Colima manually")
	}

	args := []string{"start"}

	if _, err := run("colima", args...); err != nil {
		return "", fmt.Errorf("failed to start Colima: %w", err)
	}

	// Wait for Docker
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := run("docker", "info"); err == nil {
			return "started", nil
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("docker did not become ready after starting Colima")
}

// simple dotenv parser (KEY=VALUE lines)
func readDotenv(path string) map[string]string {
	env := map[string]string{}
	if path == "" {
		return env
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return env
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if i := strings.IndexByte(line, '='); i > 0 {
			k := strings.TrimSpace(line[:i])
			v := strings.Trim(strings.TrimSpace(line[i+1:]), `"'`)
			env[k] = v
		}
	}
	return env
}

// ---------- Tools ----------

func registerTools() {
	tools["ensureDocker"] = Tool{
		Decl: ToolDecl{
			Name:        "ensureDocker",
			Description: "Ensure Docker is reachable. If not, start Colima and wait until Docker responds.",
			InputSchema: map[string]any{
				"type":                 "object",
				"properties":           map[string]any{}, // no inputs
				"additionalProperties": false,
			},
		},
		Call: func(a map[string]any) (string, bool, error) {
			status, err := ensureDockerReady()
			if err != nil {
				return "", true, err
			}
			return j(map[string]string{"status": status}), false, nil
		},
	}

	// composeUp
	tools["composeUp"] = Tool{
		Decl: ToolDecl{
			Name:        "composeUp",
			Description: "Start docker compose. Required: project, compose_file. Optional: build (bool)",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project":      map[string]any{"type": "string"},
					"compose_file": map[string]any{"type": "string"},
					"build":        map[string]any{"type": "boolean"}, // default false; forces image rebuild
				},
				"required":             []string{"project", "compose_file"},
				"additionalProperties": false,
			},
		},
		Call: func(a map[string]any) (string, bool, error) {
			if os.Getenv("ENSURE_DOCKER_AUTO") != "0" {
				if _, err := ensureDockerReady(); err != nil {
					return "", true, err
				}
			}

			project := a["project"].(string)
			composeFile := a["compose_file"].(string)
			build, _ := a["build"].(bool)

			if err := safeProject(project); err != nil {
				return "", true, err
			}
			if err := safeComposePath(composeFile); err != nil {
				return "", true, err
			}

			args := []string{"-p", project, "-f", composeFile}

			args = append(args, "up", "-d")
			if build {
				args = append(args, "--build")
			}

			extra := readDotenv(os.Getenv("APP_ENV_FILE"))
			out, err := runComposeWithEnv(extra, args...)
			return j(map[string]string{"output": out}), err != nil, err
		},
	}

	// composeDown
	tools["composeDown"] = Tool{
		Decl: ToolDecl{
			Name:        "composeDown",
			Description: "Stop compose. Required: project, compose_file. Optional: build (bool).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project":        map[string]any{"type": "string"},
					"compose_file":   map[string]any{"type": "string"},
					"remove_volumes": map[string]any{"type": "boolean"},
				},
				"required":             []string{"project", "compose_file"},
				"additionalProperties": false,
			},
		},
		Call: func(a map[string]any) (string, bool, error) {
			if os.Getenv("ENSURE_DOCKER_AUTO") != "0" {
				if _, err := ensureDockerReady(); err != nil {
					return "", true, err
				}
			}

			project := a["project"].(string)
			composeFile := a["compose_file"].(string)

			// Detect if caller explicitly set remove_volumes
			rmvol := false
			if v, ok := a["remove_volumes"]; ok {
				if b, ok2 := v.(bool); ok2 {
					rmvol = b
				}
			} else {
				// Not provided → ask interactively (default: no)
				rmvol = askYesNo("Also delete named volumes? [y/N]: ", false)
			}

			if err := safeProject(project); err != nil {
				return "", true, err
			}
			if err := safeComposePath(composeFile); err != nil {
				return "", true, err
			}

			args := []string{"-p", project, "-f", composeFile, "down"}
			if rmvol {
				args = append(args, "-v")
			}

			extra := readDotenv(os.Getenv("APP_ENV_FILE"))
			out, err := runComposeWithEnv(extra, args...)
			return j(map[string]string{"output": out}), err != nil, err
		},
	}

	// waitHealthy
	tools["waitHealthy"] = Tool{
		Decl: ToolDecl{
			Name:        "waitHealthy",
			Description: "Poll container health until healthy. Required: project, service. Optional: timeout_sec, compose_file.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project":      map[string]any{"type": "string"},
					"service":      map[string]any{"type": "string"},
					"timeout_sec":  map[string]any{"type": "integer"},
					"compose_file": map[string]any{"type": "string"},
				},
				"required":             []string{"project", "service"},
				"additionalProperties": false,
			},
		},
		Call: func(a map[string]any) (string, bool, error) {
			if os.Getenv("ENSURE_DOCKER_AUTO") != "0" {
				if _, err := ensureDockerReady(); err != nil {
					return "", true, err
				}
			}

			project := a["project"].(string)
			service := a["service"].(string)
			composeFile, _ := a["compose_file"].(string)
			tout, _ := a["timeout_sec"].(float64)
			if tout == 0 {
				tout = 180
			}

			if err := safeProject(project); err != nil {
				return "", true, err
			}
			if composeFile != "" {
				if err := safeComposePath(composeFile); err != nil {
					return "", true, err
				}
			}

			id, err := containerID(project, composeFile, service)
			if err != nil {
				return j(map[string]string{"status": "not-found"}), true, err
			}

			deadline := time.Now().Add(time.Duration(tout) * time.Second)
			for time.Now().Before(deadline) {
				out, _ := run("docker", "inspect", "--format", "{{.State.Health.Status}}", id)
				if strings.Contains(out, "healthy") {
					return j(map[string]string{"status": "healthy"}), false, nil
				}
				time.Sleep(3 * time.Second)
			}
			return j(map[string]string{"status": "timeout"}), true, errors.New("service not healthy in time")
		},
	}

	tools["serviceLogs"] = Tool{
		Decl: ToolDecl{
			Name:        "serviceLogs",
			Description: "Return the last N lines of logs for a service. Required: project, service. Optional: compose_file, tail.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project":      map[string]any{"type": "string"},
					"service":      map[string]any{"type": "string"},
					"compose_file": map[string]any{"type": "string"},
					"tail":         map[string]any{"type": "integer"},
				},
				"required":             []string{"project", "service"},
				"additionalProperties": false,
			},
		},
		Call: func(a map[string]any) (string, bool, error) {
			if os.Getenv("ENSURE_DOCKER_AUTO") != "0" {
				if _, err := ensureDockerReady(); err != nil {
					return "", true, err
				}
			}

			project := a["project"].(string)
			service := a["service"].(string)
			composeFile, _ := a["compose_file"].(string)
			tailF, _ := a["tail"].(float64)
			if tailF == 0 {
				tailF = 200
			}

			if err := safeProject(project); err != nil {
				return "", true, err
			}
			if composeFile != "" {
				if err := safeComposePath(composeFile); err != nil {
					return "", true, err
				}
			}

			id, err := containerID(project, composeFile, service)
			if err != nil {
				return j(map[string]string{"status": "not-found"}), true, err
			}

			out, err := run("docker", "logs", "--tail", fmt.Sprint(int(tailF)), id)
			return j(map[string]string{"logs": out}), err != nil, err
		},
	}

	// dbReset
	tools["dbReset"] = Tool{
		Decl: ToolDecl{
			Name:        "dbReset",
			Description: `Destructive: reset DB by 'compose down -v' then 'up -d'. Removes containers, network, and named volumes (data is lost). Requires confirm_phrase="RESET <project>". After starting, waits for the service to become healthy. Optional: seed_cmd.`, InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"project":        map[string]any{"type": "string"},
					"compose_file":   map[string]any{"type": "string"},
					"db_service":     map[string]any{"type": "string"},
					"seed_cmd":       map[string]any{"type": "string"},
					"confirm_phrase": map[string]any{"type": "string"},
				},
				"required":             []string{"project", "compose_file", "db_service", "confirm_phrase"},
				"additionalProperties": false,
			},
		},
		Call: func(a map[string]any) (string, bool, error) {
			if os.Getenv("ENSURE_DOCKER_AUTO") != "0" {
				if _, err := ensureDockerReady(); err != nil {
					return "", true, err
				}
			}

			project := a["project"].(string)
			compose := a["compose_file"].(string)
			dbSvc := a["db_service"].(string)
			seed, _ := a["seed_cmd"].(string)
			confirm, _ := a["confirm_phrase"].(string)

			if err := safeProject(project); err != nil {
				return "", true, err
			}
			if err := safeComposePath(compose); err != nil {
				return "", true, err
			}

			expect := "RESET " + project
			if confirm != expect {
				return "", true, fmt.Errorf("confirmation mismatch; expected %q", expect)
			}

			extra := readDotenv("APP_ENV_FILE")

			if _, err := runComposeWithEnv(extra, "-p", project, "-f", compose, "down", "-v"); err != nil {
				return "", true, err
			}

			args := []string{"-p", project, "-f", compose}

			args = append(args, "up", "-d")
			if _, err := runComposeWithEnv(extra, args...); err != nil {
				return "", true, err
			}

			if _, _, err := tools["waitHealthy"].Call(map[string]any{
				"project": project, "service": dbSvc, "timeout_sec": 180, "compose_file": compose,
			}); err != nil {
				return "", true, err
			}

			seedOut := ""
			if strings.TrimSpace(seed) != "" {
				out, err := runComposeWithEnv(extra, "-p", project, "-f", compose, "exec", "-T", dbSvc, "sh", "-lc", seed)
				if err != nil {
					return "", true, err
				}
				seedOut = out
			}
			return j(map[string]string{"status": "reset-complete", "seed_out": seedOut}), false, nil
		},
	}
}
