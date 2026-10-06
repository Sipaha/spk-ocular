package compose

import (
	"github.com/spk/spk-ocular/internal/core"
)

// The provider's English texts for the UI by key; the UI says the keys it
// knows in its language (web/src/i18n.ts, providerTexts) with the same
// parameters, the rest in this English.
var messageTexts = map[string]string{
	"scope.singular":                  "Project",
	"scope.plural":                    "projects",
	"scope.all":                       "All resources",
	"container.exited":                "exit code {code}",
	"container.exitedOOM":             "exit code {code}, killed for running out of memory",
	"container.restarting":            "restarting after exit code {code}",
	"container.unknownState":          "unknown state {state}",
	"container.restartedByPolicy":     "restarted by its restart policy ({count} restarts in total)",
	"container.restartedByPolicyOnce": "restarted by its restart policy ({count} restart in total)",
	"service.members":                 "{count} of {total} containers",
	"service.membersExample":          "{count} of {total} containers; {example}",
	"volume.identity":                 "A volume is identified by its name and creation time (to the second): one re-created within the same second is taken for the same volume.",
	"error.unknownKind":               "unknown kind {kind}",
	"error.notObserved":               "the {feed} are not observed",
	"error.notFound":                  "{kind} {name} is not found",
	"error.gone":                      "{kind} {name} no longer exists: another object has its name now",
	"logs.stream":                     "Stream",
	"logs.allStreams":                 "stdout and stderr",
	"level.container":                 "Container",
	"exec.noRunning":                  "The service has no running containers",
	"exec.notRunning":                 "The container is {state}",
	"exec.sizeNotSet":                 "The terminal size ({cols}×{rows}) could not be set in the container; resizing the window sets it.",
	"act.removing":                    "The container is being removed",
	"act.dead":                        "The container is dead: it can only be removed",
	"act.startPaused":                 "The container is paused: unpause it first (not offered here)",
	"act.running":                     "The container is running already",
	"act.notRunning":                  "The container is not running ({state})",
	"act.stopFirst":                   "The container is {state}: stop it first",
	"act.start":                       "Starts {name}.",
	"act.stop":                        "Stops {name} with {signal}; if it has not exited after {timeout} s, it is killed (SIGKILL).",
	"act.stopKill":                    "Kills {name} at once (SIGKILL): its stop timeout is 0.",
	"act.stopWait":                    "Stops {name} with {signal} and waits for it to exit: its stop timeout is −1, it is never killed.",
	"act.autoRemove":                  "{name} is removed once it stops, with its anonymous volumes (AutoRemove).",
	"act.policyAlways":                "{name} has the restart policy always: it stays stopped until started again or until the Docker daemon restarts.",
	"act.restart":                     "Restarts {name}: stops it with {signal} (killed after {timeout} s if still running), then starts it.",
	"act.restartKill":                 "Restarts {name}: kills it at once (SIGKILL, stop timeout 0), then starts it.",
	"act.restartWait":                 "Restarts {name}: stops it with {signal} and waits without killing (stop timeout −1), then starts it.",
	"act.restartStopped":              "Starts {name} (it is not running).",
	"act.remove":                      "Removes {name}; its volumes (anonymous ones too) and its image are kept.",
	"act.notTouched":                  "{name} is not touched: {reason}.",
	"act.noPrecondition":              "A change since this review is detected: the run reads the containers again before its first write. The Docker Engine takes no preconditions, so a change after that check (while a service's containers are acted on one by one) is not.",
	"act.newMembers":                  "Only the containers listed here are acted on, one after another: a change of the set before the run refuses it; containers the service gets while it runs are not touched.",
	"error.containerRemoved":          "the container was removed",
	"error.containerReplaced":         "the container was removed and another took its name",
	"act.noAction":                    "{kind} cannot be {action}",
	"act.notServiceKey":               "{key} is not a service key (project/service)",
	"act.noContainers":                "the service has no containers",
	"act.changedContainer":            "container {name} changed since the action was reviewed; review it again",
	"act.changedService":              "service {name} changed since the action was reviewed; review it again",
	"act.removedMeanwhile":            "container {name} was removed meanwhile",
	"act.engineRefused":               "the Docker Engine refused: {detail}",
	"done.start":                      "container {name} started",
	"done.stop":                       "container {name} stopped",
	"done.restart":                    "container {name} restarted",
	"done.delete":                     "container {name} removed",
	"done.alreadyRunning":             "container {name} was running already",
	"done.alreadyStopped":             "container {name} was stopped already",
	"act.serviceNone.start":           "Every container of {service} is running (or paused)",
	"act.serviceNone.stop":            "No container of {service} is running",
	"act.serviceNone.restart":         "No container of {service} can be restarted",
}

// msg builds the message key with params given as name, value pairs.
func msg(key string, kv ...string) core.Message {
	tmpl, ok := messageTexts[key]
	if !ok {
		panic("compose: no text for " + key)
	}
	m := core.Message{Key: ProviderID + "." + key, Text: tmpl}
	if len(kv) > 0 {
		m.Params = make(map[string]string, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			m.Params[kv[i]] = kv[i+1]
		}
		m.Text = core.Format(tmpl, m.Params)
	}
	return m
}
