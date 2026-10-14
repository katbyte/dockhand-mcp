package workarounds

import (
	pandorest "github.com/katbyte/pandorest/importer/workarounds"
	"github.com/katbyte/pandorest/openapi"
)

// jobAccept gives the calls Dockhand answers two ways the header it chooses between them by.
type jobAccept struct{}

func (jobAccept) Name() string    { return Service + "-job-accept" }
func (jobAccept) Service() string { return Service }

func (jobAccept) Bug() string {
	return "pulling an image, pushing one, pruning images, scanning one image or every image of an environment, making a stack, saving a stack's compose file, starting, stopping, restarting, deploying and taking down a stack, deploying a git stack with its progress, running a backup, restoring one, and looking after or verifying a backup store answer one of two ways, and the document declares no parameter that chooses: a caller whose Accept header holds application/json and not text/event-stream is made to wait and answered how it went, and any other is answered {jobId} at once, for GET /api/jobs/{id} to follow. The git stack's call is the one exception to the first half: made to wait, it answers only that there was no result, so it is only of use as a job"
}

func (jobAccept) Apply(spec *openapi.Spec) error {
	return pandorest.AddParameters(spec, []string{
		"POST /api/images/pull",
		"POST /api/images/push",
		"POST /api/images/scan",
		"POST /api/vulnerabilities/scan-all",
		"POST /api/prune/images",
		"POST /api/stacks",
		"PUT /api/stacks/{name}/compose",
		"POST /api/stacks/{name}/start",
		"POST /api/stacks/{name}/stop",
		"POST /api/stacks/{name}/restart",
		"POST /api/stacks/{name}/deploy",
		"POST /api/stacks/{name}/down",
		"POST /api/git/stacks/{id}/deploy-stream",
		"POST /api/backup/configs/{id}/run",
		"POST /api/backup/restore",
		"POST /api/backup/destinations/{id}/task",
		"POST /api/backup/destinations/{id}/verify",
	}, pandorest.Param{Name: "Accept", In: openapi.InHeader, Type: openapi.TypeString, Description: "what the caller will take for an answer. Left out, the call waits for the work and answers how it went; AcceptJob has it answered with the id of a job at once"})
}
