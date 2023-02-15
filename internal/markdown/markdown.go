package markdown

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rneatherway/gh-slack/internal/slackclient"
)

var userRE = regexp.MustCompile("<@[A-Z0-9]+>")
var linkRE = regexp.MustCompile(`<(https?://[^|>]+)\|([^>]+)>`)
var openCodefence = regexp.MustCompile("(?m)^```")
var closeCodefence = regexp.MustCompile("(?m)(.)```$")

type UserProvider interface {
	UsernameForID(string) (string, error)
}

func interpolateUsers(client UserProvider, s string) (string, error) {
	userLocations := userRE.FindAllStringIndex(s, -1)
	out := &strings.Builder{}
	last := 0
	for _, userLocation := range userLocations {
		start := userLocation[0]
		end := userLocation[1]

		username, err := client.UsernameForID(s[start+2 : end-1])
		if err != nil {
			return "", err
		}
		out.WriteString(s[last:start])
		out.WriteString("`@")
		out.WriteString(username)
		out.WriteRune('`')
		last = end
	}
	out.WriteString(s[last:])

	return out.String(), nil
}

func parseUnixTimestamp(s string) (*time.Time, error) {
	tsParts := strings.Split(s, ".")
	if len(tsParts) != 2 {
		return nil, fmt.Errorf("timestamp '%s' in not in <seconds>.<milliseconds> format", s)
	}

	seconds, err := strconv.ParseInt(tsParts[0], 10, 64)
	if err != nil {
		return nil, err
	}

	nanos, err := strconv.ParseInt(tsParts[1], 10, 64)
	if err != nil {
		return nil, err
	}

	result := time.Unix(seconds, nanos)
	return &result, nil
}

func convert(client UserProvider, b *strings.Builder, s string) error {
	text, err := interpolateUsers(client, s)
	if err != nil {
		return err
	}

	text = linkRE.ReplaceAllString(text, "[$2]($1)")
	text = openCodefence.ReplaceAllString(text, "```\n")
	text = closeCodefence.ReplaceAllString(text, "$1\n```")

	for _, line := range strings.Split(text, "\n") {
		// TODO: Might be a good idea to escape 'line'
		fmt.Fprintf(b, "> %s\n", line)
	}

	return nil
}

func FromMessages(team string, channelID string, client *slackclient.SlackClient, history *slackclient.HistoryResponse) (string, error) {
	b := &strings.Builder{}
	messages := history.Messages
	msgTimes := make(map[string]time.Time, len(messages))

	for _, message := range messages {
		tm, err := parseUnixTimestamp(message.Ts)
		if err != nil {
			return "", err
		}

		msgTimes[message.Ts] = *tm
	}

	// It's surprising that these messages are not already always returned in date order,
	// and actually I observed initially that they seemed to be, but at least some of the
	// time they are returned in reverse order so it's simpler to just sort them now.
	sort.Slice(messages, func(i, j int) bool {
		return msgTimes[messages[i].Ts].Before(msgTimes[messages[j].Ts])
	})

	firstMessage := messages[0]

	for _, message := range messages {
		var username string
		var err error

		if message.User != "" {
			username, err = client.UsernameForID(message.User)
		} else if message.BotID != "" {
			username = fmt.Sprintf("bot %s", message.BotID)
		} else {
			username = "ghost"
		}
		if err != nil {
			return "", err
		}

		fmt.Fprintf(b, "> %s at [%s](%s)\n>\n",
			formatUserProfile(username),
			msgTimes[message.Ts].Format("2006-01-02 15:04"),
			formatLinkToMessage(team, channelID, firstMessage, message),
		)

		if message.Text != "" {
			err = convert(client, b, message.Text)
			if err != nil {
				return "", err
			}
		}

		// These seem to be mostly bot messages so far. Perhaps we should just skip them?
		for _, a := range message.Attachments {
			err = convert(client, b, a.Text)
			if err != nil {
				return "", err
			}
		}

		b.WriteString("\n")
	}

	return b.String(), nil
}

func formatUserProfile(username string) string {
	return fmt.Sprintf(`[<img src="https://github.com/%s.png?size=25" align="left" /> **%s**](https://github.com/%s)`, username, username, username)
}

func formatLinkToMessage(team, channelID string, firstMessage slackclient.Message, threadMessage slackclient.Message) string {
	return fmt.Sprintf("https://%s.slack.com/archives/%s/p%s?thread_ts=%s&cid=%s",
		team,
		channelID,
		firstMessage.Ts,
		threadMessage.Ts,
		channelID,
	)
}

func WrapInDetails(channelName, link, s string) string {
	return fmt.Sprintf("Slack conversation archive of [`#%s`](%s)\n\n<details>\n  <summary>Click to expand</summary>\n\n%s\n</details>",
		channelName, link, s)
}
