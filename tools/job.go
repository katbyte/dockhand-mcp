package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/katbyte/dockhand-mcp/sdk/dockhand"
	"github.com/katbyte/pandorest/client"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// How a job Dockhand runs in the background stands, in its own words.
const (
	jobRunning = "running"
	jobDone    = "done"
	jobFailed  = "error"
)

const (
	// defaultWait is how long a tool waits for work it started before it answers with the work still going: under the minute many clients give a tool
	defaultWait = 50 * time.Second
	// maxWait is the longest a tool may be asked to wait
	maxWait = 300 * time.Second
	// jobPoll is how often a job is asked how it goes
	jobPoll = 500 * time.Millisecond
	// progressKept is how many of a job's last lines a tool shows
	progressKept = 5
)

// waitArg is the argument of a tool that starts work Dockhand runs in the background: pulling an image, pruning images.
type waitArg struct {
	Wait *int `json:"wait,omitempty" jsonschema:"how many seconds to wait for the work to finish before answering with it still running; 50 unless said, 300 at most, and 0 to start it and answer at once"`
}

// patience is how long the argument says to wait.
func (a waitArg) patience() (time.Duration, error) {
	if a.Wait == nil {
		return defaultWait, nil
	}
	wait := time.Duration(*a.Wait) * time.Second
	if wait < 0 || wait > maxWait {
		return 0, fmt.Errorf("wait is %d: it is the seconds to wait, from 0 to %d", *a.Wait, int(maxWait.Seconds()))
	}

	return wait, nil
}

// jobState is how work Dockhand runs in the background stands, as a tool that started it or asked after it answers.
type jobState struct {
	Job      string   `json:"job,omitempty"      jsonschema:"the id of the job the work runs as, while it is still running: job_get says how it goes"`
	Running  bool     `json:"running"            jsonschema:"whether the work is still going. True means this answer came before the work ended, and what the work was to do is not done yet"`
	Progress []string `json:"progress,omitempty" jsonschema:"the last few things the work reported, oldest first"`
}

// job is a job as Dockhand answers one.
type job = dockhand.GetJobsByIdAnswer

// stateOfJob is what a tool says of a job: its id while it runs, and the last of what it reported.
func stateOfJob(j *job) jobState {
	state := jobState{Running: j.Status == jobRunning, Progress: progressOf(j.Lines)}
	if state.Running {
		state.Job = j.Id
	}

	return state
}

// progressOf is the last few things a job reported, in words: a layer and what is being done with it for a pull, and whatever text a line carries for
// anything else. A line that says the same as the one before it, as a layer's download does many times over, is said once.
func progressOf(lines []dockhand.GetJobsByIdAnswerLines) []string {
	var said []string
	for i := range lines {
		if lines[i].Event == "result" {
			continue
		}
		line := progressLine(lines[i].Data)
		if line == "" || (len(said) > 0 && said[len(said)-1] == line) {
			continue
		}
		said = append(said, line)
	}
	if len(said) > progressKept {
		said = said[len(said)-progressKept:]
	}

	return said
}

// textOf is a value of a job's free-form answer as text, trimmed: nothing for one that is not text.
func textOf(v any) string {
	s, isText := v.(string)
	if !isText {
		return ""
	}

	return strings.TrimSpace(s)
}

// progressLine is one line of a job's progress in words, or nothing for a line that says nothing a reader can use.
func progressLine(data map[string]any) string {
	var parts []string
	// Docker's own report of a pull names the layer first; what compose prints comes a line at a time, as line or, deploying from git, as logLine
	for _, key := range []string{"id", "status", "line", "logLine", "message", "output", "error"} {
		if part := textOf(data[key]); part != "" && !slices.Contains(parts, part) {
			parts = append(parts, part)
		}
	}

	return strings.Join(parts, ": ")
}

// await asks a job how it goes until it has ended or the patience given has run out, and answers how it then stands. A job that cannot be asked is an
// error that names it, so that it can be asked again.
func (r *registry) await(ctx context.Context, id string, patience time.Duration) (*job, error) {
	deadline := time.Now().Add(patience)
	for {
		answer, err := r.dock.GetJobsById(ctx, id)
		if client.IsNotFound(err) {
			return nil, fmt.Errorf("no job %s is known to Dockhand: it keeps a job for a while after it ends and then forgets it, and shows one only to whoever started it", id)
		}
		if err != nil {
			return nil, fmt.Errorf("job %s could not be asked how it goes: %w", id, refusal(err))
		}
		if answer.Answer.Status == "" {
			return nil, fmt.Errorf("job %s was asked how it goes and Dockhand answered nothing that says", id)
		}
		if answer.Answer.Status != jobRunning || !time.Now().Add(r.poll()).Before(deadline) {
			return answer.Answer, nil
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("job %s was still running when this call was ended: %w", id, ctx.Err())
		case <-time.After(r.poll()):
		}
	}
}

// poll is how often a job is asked how it goes.
func (r *registry) poll() time.Duration {
	if r.opts.poll > 0 {
		return r.opts.poll
	}

	return jobPoll
}

// seconds is a wait in words.
func seconds(wait time.Duration) string {
	if wait == time.Second {
		return "1 second"
	}

	return fmt.Sprintf("%d seconds", int(wait.Seconds()))
}

// wrapped is Docker's own refusal as Dockhand passes it on: a sentence of Dockhand's, and then Docker's words as the JSON they came in.
var wrapped = regexp.MustCompile(`^(?:Error: )?(.*?):? *\{"message":("(?:[^"\\]|\\.)*")\}\s*$`)

// plainly is why something failed with Docker's words taken out of the JSON Dockhand hands them on in, so that a reason reads as one.
func plainly(why string) string {
	why = strings.TrimSpace(why)
	m := wrapped.FindStringSubmatch(why)
	if m == nil {
		return strings.TrimPrefix(why, "Error: ")
	}
	var said string
	if err := json.Unmarshal([]byte(m[2]), &said); err != nil {
		return why
	}
	if m[1] == "" {
		return said
	}

	return m[1] + ": " + said
}

// failureOf is why a job that ended in an error says it did: its result's own words, or the last thing it reported.
func failureOf(j *job) string {
	for _, key := range []string{"error", "message", "output"} {
		// compose says why it stopped last, after every step it took on the way
		if why := textOf(j.Result[key]); why != "" {
			return lastLine(why)
		}
	}
	if said := progressOf(j.Lines); len(said) > 0 {
		return said[len(said)-1]
	}

	return "Dockhand gave no reason"
}

// failed reports whether a job that has ended did not do what it was for: it ended in an error, or it ended and its result says so.
func failed(j *job) bool {
	if j.Status == jobFailed {
		return true
	}
	if success, said := j.Result["success"].(bool); said && !success {
		return true
	}

	return textOf(j.Result["status"]) == jobFailed
}

func registerJobTools(r *registry) {
	type jobIn struct {
		waitArg

		Job string `json:"job" jsonschema:"the job's id, as the tool that started it gave it"`
	}
	type jobOut struct {
		jobState

		ID     string         `json:"id"               jsonschema:"the job's id"`
		Status string         `json:"status"           jsonschema:"running, done, or error for a job that ended without doing what it was for"`
		Result map[string]any `json:"result,omitempty" jsonschema:"what the job answered when it ended, as Dockhand gives it: what was done, or why not under error"`
		Error  string         `json:"error,omitempty"  jsonschema:"why the job did not do what it was for, when it did not"`
		Note   string         `json:"note,omitempty"`
	}
	add(r, readTool, &mcp.Tool{
		Name:        "job_get",
		Description: "Ask how work Dockhand runs in the background goes, by the job id a tool gave when it answered before its work ended: an image being pulled, images being pruned. Answers whether it is running, done or ended in an error, the last few things it reported, and once it has ended what it answered or why it failed. Give wait to hold the answer until the job ends or that many seconds pass; left out, it answers at once. Dockhand keeps a job for a while after it ends and then forgets it, and shows one only to the user who started it.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in jobIn) (*mcp.CallToolResult, jobOut, error) {
		id := strings.TrimSpace(in.Job)
		if id == "" {
			return nil, jobOut{}, errors.New("job is required: the id a tool gave for work it left running")
		}
		// asking after a job answers at once unless told to wait
		patience := time.Duration(0)
		if in.Wait != nil {
			var err error
			if patience, err = in.patience(); err != nil {
				return nil, jobOut{}, err
			}
		}

		j, err := r.await(ctx, id, patience)
		if err != nil {
			return nil, jobOut{}, err
		}

		out := jobOut{jobState: stateOfJob(j), ID: j.Id, Status: string(j.Status), Result: j.Result}
		switch {
		case out.Running:
			out.Note = "still running: ask again, with wait to hold the answer until it ends"
		case failed(j):
			out.Status, out.Error = jobFailed, failureOf(j)
		default:
		}

		return nil, out, nil
	})

	type jobCancelIn struct {
		Job string `json:"job" jsonschema:"the job's id, as the tool that started it gave it"`
	}
	type jobCancelOut struct {
		ID        string `json:"id"             jsonschema:"the job's id"`
		Was       string `json:"was"            jsonschema:"how the job stood before: running, done or error"`
		Is        string `json:"is"             jsonschema:"how it stands after, read back: a job asked to stop may run on for a while, or to its end"`
		Cancelled bool   `json:"cancelled"      jsonschema:"whether Dockhand took the request to stop it"`
		Note      string `json:"note,omitempty"`
	}
	add(r, writeTool, &mcp.Tool{
		Name:        "job_cancel",
		Description: "Ask Dockhand to stop work it runs in the background, by its job id. It is a request: Dockhand marks the job to stop, and work already handed to Docker, a pull under way above all, commonly runs to its end all the same, so the answer reads the job back and says how it stands. A job that has already ended is left as it is and said to have ended.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in jobCancelIn) (*mcp.CallToolResult, jobCancelOut, error) {
		id := strings.TrimSpace(in.Job)
		if id == "" {
			return nil, jobCancelOut{}, errors.New("job is required: the id a tool gave for work it left running")
		}

		before, err := r.await(ctx, id, 0)
		if err != nil {
			return nil, jobCancelOut{}, err
		}
		out := jobCancelOut{ID: id, Was: string(before.Status), Is: string(before.Status)}
		if before.Status != jobRunning {
			out.Note = "the job had already ended, so there was nothing to stop"

			return nil, out, nil
		}

		answer, err := r.dock.DeleteJobsById(ctx, id)
		if err != nil {
			return nil, out, fmt.Errorf("job %s could not be asked to stop: %w", id, refusal(err))
		}
		out.Cancelled = answer.Answer.Cancelled

		after, err := r.await(ctx, id, 0)
		if err != nil {
			return nil, out, err
		}
		if out.Is = string(after.Status); out.Is == jobRunning {
			out.Note = "asked to stop and still running: work Docker has already begun, a pull above all, is commonly finished all the same; job_get says how it ends"
		}

		return nil, out, nil
	})
}
