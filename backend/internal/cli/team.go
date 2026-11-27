package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

func newTeamCommand(c *commandContext) *cobra.Command {
	var projectID string
	root := &cobra.Command{Use: "team", Short: "Manage project chiefs, missions, roles, mail, and durable memory"}
	root.PersistentFlags().StringVar(&projectID, "project", "", "Project ID (defaults to the current AO session or repository)")
	for _, operation := range []string{"status", "inbox", "export", "enable"} {
		operation := operation
		command := &cobra.Command{Use: operation, Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
			return c.teamAction(cmd, projectID, operation, json.RawMessage(`{}`))
		}}
		root.AddCommand(command)
	}
	var payload, file string
	act := &cobra.Command{Use: "act OPERATION", Short: "Apply a team action using a JSON argument object", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		if file != "" {
			var data []byte
			var err error
			if file == "-" {
				data, err = io.ReadAll(c.deps.In)
			} else {
				data, err = os.ReadFile(file)
			}
			if err != nil {
				return err
			}
			payload = string(data)
		}
		var object map[string]any
		if err := json.Unmarshal([]byte(payload), &object); err != nil || object == nil {
			return usageError{fmt.Errorf("team arguments must be a JSON object")}
		}
		return c.teamAction(cmd, projectID, args[0], json.RawMessage(payload))
	}}
	act.Flags().StringVar(&payload, "json", "{}", "JSON object of operation arguments")
	act.Flags().StringVar(&file, "file", "", "JSON argument file, or - for stdin")
	act.MarkFlagsMutuallyExclusive("json", "file")
	root.AddCommand(act)
	root.AddCommand(&cobra.Command{Use: "hook EVENT", Hidden: true, Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		session := strings.TrimSpace(os.Getenv("AO_SESSION_ID"))
		if !sessionIDPattern.MatchString(session) {
			_, err := fmt.Fprintln(c.deps.Out, "{}")
			return err
		}
		if _, err := io.Copy(io.Discard, c.deps.In); err != nil {
			return err
		}
		// The launch identity proves this callback belongs to the live
		// provider generation: the supervised terminal launch id for TUI,
		// the chat host boot id for Chat. Callbacks that predate it send ""
		// and stay accepted for backward compatibility.
		body := map[string]string{"event": args[0]}
		if launchID := validLaunchID(os.Getenv("AO_RUNTIME_LAUNCH_ID")); launchID != "" {
			body["launchId"] = launchID
		}
		var response struct {
			Result json.RawMessage `json:"result"`
		}
		if err := c.postJSON(cmd.Context(), "sessions/"+url.PathEscape(session)+"/team-hook", body, &response); err != nil {
			return err
		}
		_, err := fmt.Fprintln(c.deps.Out, string(response.Result))
		return err
	}})
	return root
}

func (c *commandContext) teamAction(cmd *cobra.Command, projectID, operation string, arguments json.RawMessage) error {
	project, err := c.resolveSpawnProject(cmd.Context(), projectID)
	if err != nil {
		return err
	}
	var response struct {
		Result json.RawMessage `json:"result"`
	}
	request := struct {
		Operation string          `json:"operation"`
		SessionID string          `json:"sessionId,omitempty"`
		Arguments json.RawMessage `json:"arguments"`
	}{operation, strings.TrimSpace(os.Getenv("AO_SESSION_ID")), arguments}
	if err := c.postJSON(cmd.Context(), "projects/"+url.PathEscape(project.ID)+"/team", request, &response); err != nil {
		return err
	}
	var output any
	if err := json.Unmarshal(response.Result, &output); err != nil {
		return err
	}
	encoder := json.NewEncoder(c.deps.Out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(output)
}
