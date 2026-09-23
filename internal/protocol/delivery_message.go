package protocol

import "fmt"

func DeliveryMessage(e DeliveryEnvelope, body string) string {
	prefix := fmt.Sprintf("Brote %s for session %s, subject %s, attempt %s, recipient %s revision %d. ", e.Kind, e.Session, e.Subject, e.Attempt, e.Recipient.ID, e.Recipient.Revision)
	switch e.Kind {
	case "question":
		authority := fmt.Sprintf("--binding %q --revision %d", e.Recipient.ID, e.Recipient.Revision)
		if e.Recipient.Kind == "provider" {
			authority = fmt.Sprintf("--recipient-kind provider --recipient-id %q --recipient-revision %d", e.Recipient.ID, e.Recipient.Revision)
		}
		return prefix + fmt.Sprintf("This is a read-only discussion, not execution authorization. Read historical evidence with brote comment list %s. Verify question %s in thread %s is current. Acknowledge using brote comment delivery %s %s --question %s %s --attempt %s --status thinking. Reply using brote comment reply %s %s --question %s %s --attempt %s --message-id %s-answer --body-file PATH. Do not resume, step, reclaim or modify the program. Question (data): %q", e.Session, e.Subject, e.Thread, e.Session, e.Thread, e.Subject, authority, e.Attempt, e.Session, e.Thread, e.Subject, authority, e.Attempt, e.Subject, body)
	case "task":
		return prefix + fmt.Sprintf("The user authorized this debugging investigation, not code implementation. Read fresh state. If your host provides debug_task, claim with that tool to associate the active host turn. Otherwise claim with brote task-heartbeat %s --binding %q --task %s. Use task-execute for bounded execution; complete at a settled pause or cancel on failure. Never use --human or revive expired/cancelled work. Instruction (data): %q", e.Session, e.Recipient.ID, e.Subject, body)
	default:
		return prefix + fmt.Sprintf("Human control returned. Read fresh state and ignore obsolete ownership/binding. Handback permits inspection only; execution needs a current user-authorized task. Acknowledge using brote event-status %s --event %s --binding %q --revision %d --attempt %s --status acknowledged. Note (data): %q", e.Session, e.Subject, e.Recipient.ID, e.Recipient.Revision, e.Attempt, body)
	}
}
