package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/url"
	"time"
)

type backgroundTaskView struct {
	AuditID         int64  `json:"audit_id"`
	OperationID     string `json:"operation_id"`
	Cluster         string `json:"cluster"`
	Instance        string `json:"instance"`
	Session         string `json:"session"`
	CommandSummary  string `json:"command_summary"`
	Status          string `json:"status"`
	StartedAt       string `json:"started_at"`
	FinishedAt      string `json:"finished_at,omitempty"`
	TaskID          string `json:"task_id,omitempty"`
	CancelRequested bool   `json:"cancel_requested"`
	Output          string `json:"output,omitempty"`
}

func backgroundTaskQuery(server Server) url.Values {
	query := url.Values{}
	query.Set("cluster", server.Cluster)
	query.Set("instance", server.Instance)
	return query
}

func runTaskCommand(args []string, servers []Server, configErr error, out io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(out, "Usage: doops task list --target <t>")
		fmt.Fprintln(out, "       doops task status --target <t> --id <audit-id> [--follow]")
		fmt.Fprintln(out, "       doops task stop --target <t> --id <audit-id>")
		return 2
	}
	sub := args[0]
	if sub == "--help" || sub == "-h" || sub == "help" {
		fmt.Fprintln(out, "Usage: doops task list --target <t>")
		fmt.Fprintln(out, "       doops task status --target <t> --id <audit-id> [--follow]")
		fmt.Fprintln(out, "       doops task stop --target <t> --id <audit-id>")
		return 0
	}
	fs := flag.NewFlagSet("task "+sub, flag.ContinueOnError)
	fs.SetOutput(out)
	target := fs.String("target", "", "Configured target the task runs on")
	id := fs.Int64("id", 0, "Background task audit id")
	follow := fs.Bool("follow", false, "Poll task output until it reaches a terminal state")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if *target == "" {
		fmt.Fprintln(out, "Error: --target is required")
		return 2
	}
	requireConfig(configErr)
	server := findServer(servers, *target)
	if server == nil {
		fmt.Fprintln(out, targetNotFound(servers, *target))
		return 1
	}
	token := mustResolveToken(*server)

	switch sub {
	case "list":
		raw, err := gatewayAdminRequest("GET", server.Gateway, token,
			"/v1/background-tasks", backgroundTaskQuery(*server), nil)
		if err != nil {
			fmt.Fprintln(out, "Error:", err)
			return 1
		}
		var list struct {
			Tasks []backgroundTaskView `json:"tasks"`
		}
		if err := json.Unmarshal(raw, &list); err != nil {
			fmt.Fprintln(out, "Error:", err)
			return 1
		}
		if len(list.Tasks) == 0 {
			fmt.Fprintln(out, "No background tasks.")
			return 0
		}
		for _, task := range list.Tasks {
			line := fmt.Sprintf("%d\t%s\t%s\t%s", task.AuditID, task.Status, task.StartedAt, task.CommandSummary)
			if task.TaskID != "" {
				line += "\ttask_id=" + task.TaskID
			}
			fmt.Fprintln(out, line)
		}
		return 0
	case "status":
		if *id <= 0 {
			fmt.Fprintln(out, "Error: --id is required")
			return 2
		}
		var seen int
		for {
			task, err := fetchBackgroundTask(server.Gateway, token, *id)
			if err != nil {
				fmt.Fprintln(out, "Error:", err)
				return 1
			}
			if len(task.Output) > seen {
				fmt.Fprint(out, task.Output[seen:])
				seen = len(task.Output)
			}
			if task.Status != "running" || !*follow {
				fmt.Fprintf(out, "task %d: %s\n", task.AuditID, task.Status)
				if task.Status == "running" || task.Status == "success" {
					return 0
				}
				return 1
			}
			time.Sleep(2 * time.Second)
		}
	case "stop":
		if *id <= 0 {
			fmt.Fprintln(out, "Error: --id is required")
			return 2
		}
		_, err := gatewayAdminRequest("POST", server.Gateway, token,
			fmt.Sprintf("/v1/background-tasks/%d/cancel", *id), nil, nil)
		if err != nil {
			fmt.Fprintln(out, "Error:", err)
			return 1
		}
		fmt.Fprintln(out, "Stop requested; run task status for the terminal result")
		return 0
	default:
		fmt.Fprintf(out, "unknown task command: %s\n", sub)
		return 2
	}
}

func fetchBackgroundTask(gateway, token string, id int64) (backgroundTaskView, error) {
	raw, err := gatewayAdminRequest("GET", gateway, token,
		fmt.Sprintf("/v1/background-tasks/%d", id), nil, nil)
	if err != nil {
		return backgroundTaskView{}, err
	}
	var task backgroundTaskView
	if err := json.Unmarshal(raw, &task); err != nil {
		return backgroundTaskView{}, err
	}
	return task, nil
}
