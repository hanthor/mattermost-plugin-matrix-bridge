package command

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/mattermost/mattermost/server/public/model"
)

// executeHistoryCommand previews or starts importing the channel's past
// messages. Without "confirm" it only reports what would be imported.
func (c *Handler) executeHistoryCommand(args *model.CommandArgs) *model.CommandResponse {
	reply := func(text string) *model.CommandResponse {
		return &model.CommandResponse{ResponseType: model.CommandResponseTypeEphemeral, Text: text}
	}
	confirm := false
	fromMatrix := false
	limit := 0
	for _, f := range strings.Fields(args.Command)[2:] {
		switch {
		case f == "from-matrix":
			fromMatrix = true
		case f == "confirm":
			confirm = true
		case strings.HasPrefix(f, "limit="):
			n, err := strconv.Atoi(strings.TrimPrefix(f, "limit="))
			if err != nil || n <= 0 {
				return reply("❌ `limit=` needs a positive number, for example `/matrix history confirm limit=500`.")
			}
			limit = n
		default:
			return reply("Usage: `/matrix history [from-matrix] [confirm] [limit=N]`. Without `confirm` it only shows what would be imported.")
		}
	}

	if fromMatrix {
		text, err := c.plugin.ImportMatrixHistory(args.UserId, args.ChannelId, limit, confirm)
		if err != nil {
			return reply("❌ " + err.Error())
		}
		return reply(text)
	}

	status := c.plugin.ChannelHistoryStatus(args.ChannelId)
	if status.Running {
		return reply(fmt.Sprintf("🔄 **History import is running** for this channel: %d imported, %d skipped, %d failed so far. Run `/matrix history` again later.", status.Imported, status.Skipped, status.Failed))
	}

	plan, err := c.plugin.PlanChannelHistory(args.ChannelId, limit)
	if err != nil {
		return reply("❌ " + err.Error())
	}
	if !confirm {
		return reply(historyPreview(plan, status))
	}
	if plan.Pending == 0 {
		return reply("ℹ️ Nothing to import: every message in this channel is already in the room.")
	}
	if err := c.plugin.StartChannelHistory(args.ChannelId, limit); err != nil {
		return reply("❌ " + err.Error())
	}
	return reply(fmt.Sprintf("🔄 **History import started**: %d messages, oldest first, as their authors and with their original times. It runs in the background and can be repeated if it stops. Run `/matrix history` to see progress.", plan.Pending))
}

func historyPreview(plan, last HistoryReport) string {
	var sb strings.Builder
	sb.WriteString("📜 **History import preview**\n\n")
	if plan.Pending == 0 {
		sb.WriteString("Every message in this channel is already in its Matrix room.")
	} else {
		sb.WriteString(fmt.Sprintf("%d messages would be imported, oldest first (%s to %s), as their authors and with their original times.",
			plan.Pending, time.UnixMilli(plan.Oldest).UTC().Format("2006-01-02"), time.UnixMilli(plan.Newest).UTC().Format("2006-01-02")))
		if plan.Files > 0 {
			sb.WriteString(fmt.Sprintf("\n\n%d attachments are uploaded with them (files above the size limit are skipped), and reactions are replayed with their original times.", plan.Files))
		}
		if plan.Truncated {
			sb.WriteString("\n\nThis channel has more history than one run examines; run it again after it finishes.")
		}
		sb.WriteString("\n\nRun `/matrix history confirm` to start. Add `limit=N` to import only the oldest N.")
	}
	if last.Imported+last.Failed > 0 {
		sb.WriteString(fmt.Sprintf("\n\nLast run: %d imported, %d skipped, %d failed.", last.Imported, last.Skipped, last.Failed))
		if last.Error != "" {
			sb.WriteString(" Stopped: " + last.Error)
		}
	}
	return sb.String()
}

// titleCase upper-cases the first letter of every space-separated word.
func titleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		words[i] = string(r)
	}
	return strings.Join(words, " ")
}
