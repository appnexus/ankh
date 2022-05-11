package slack

import (
	"fmt"
	"os/user"
	"strings"

	ankh "github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
	"github.com/nlopes/slack"
)

const DEFAULT_ICON_URL = "https://github.com/appnexus/ankh/blob/master/ankh.png?raw=true"
const DEFAULT_USERNAME = "ankh"

// Send out a release message based on the chart, version and environment
// supplied by the user
func PingSlackChannel(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile) error {

	// attempt the connection
	api := slack.New(ctx.AnkhConfig.Slack.Token)

	// get environment from env vs. context
	envOrContext := util.GetEnvironmentOrContext(ctx.Environment, ctx.Context)

	var messages []string
	var rollbackInstructionMessages []string

	for i := 0; i < len(ankhFile.Charts); i++ {
		chart := &ankhFile.Charts[i]

		if ctx.RollbackInstructions {
			rollbackInstructionMessage, err := getRollbackMessageText(ctx, chart, envOrContext)
			if err != nil {
				return fmt.Errorf("Unable to prompt for rollback slack message. Using default value. Error: %v", err)
			} else {
				rollbackInstructionMessages = append(rollbackInstructionMessages, rollbackInstructionMessage)
			}
		}

		message, err := getMessageText(ctx, chart, envOrContext)
		if err != nil {
			return fmt.Errorf("Unable to prompt for slack message. Using default value. Error: %v", err)
		} else {
			messages = append(messages, message)
		}
	}
	messageText := strings.Join(messages, "\n")
	rollbackInstuctionMessageText := strings.Join(rollbackInstructionMessages, "\n")

	pretext := ctx.AnkhConfig.Slack.Pretext
	if pretext == "" {
		pretext = "A new release notification has been received"
	}

	rollbackInstructionPretext := "Rollback Information for the above release"

	attachment := slack.Attachment{
		Color:   "good",
		Pretext: pretext,
		Text:    messageText,
	}

	rollbackInstructionAttachment := slack.Attachment{
		Color:   "good",
		Pretext: rollbackInstructionPretext,
		Text:    rollbackInstuctionMessageText,
	}

	icon := DEFAULT_ICON_URL
	if ctx.AnkhConfig.Slack.Icon != "" {
		icon = ctx.AnkhConfig.Slack.Icon
	}

	username := DEFAULT_USERNAME
	if ctx.AnkhConfig.Slack.Username != "" {
		username = ctx.AnkhConfig.Slack.Username
	}

	messageParams := slack.PostMessageParameters{
		IconURL:  icon,
		Username: username,
	}

	if !ctx.DryRun {
		channelId, err := getSlackChannelIDByName(api, ctx.SlackChannel)
		if err != nil {
			return err
		}

		_, _, err = api.PostMessage(channelId, slack.MsgOptionAttachments(attachment), slack.MsgOptionPostMessageParameters(messageParams))
		if ctx.RollbackInstructions {
			_, _, err = api.PostMessage(channelId, slack.MsgOptionAttachments(rollbackInstructionAttachment), slack.MsgOptionPostMessageParameters(messageParams))
		}
		return err
	} else {
		ctx.Logger.Infof("--dry-run set so not sending message '%v' to slack channel %v", messageText, ctx.SlackChannel)
	}

	return nil
}

func getSlackChannelIDByName(api *slack.Client, channelName string) (string, error) {

	params := slack.GetConversationsParameters{}
	params.ExcludeArchived = "true"
	params.Limit = 1000

	// Look for public channels and private channels the bot was invited to
	params.Types = []string{"public_channel", "private_channel"}

	channels, nextCursor, err := api.GetConversations(&params)
	if err != nil || channels == nil {
		return "", err
	}

	// Look for channel
	for _, channel := range channels {
		if channel.Name == channelName {
			return channel.ID, nil
		}
	}

	// If it doesn't exist and there are more channels, keep going
	for nextCursor != "" {
		channels, nextCursor, err = api.GetConversations(&params)
		params.Cursor = nextCursor
		for _, channel := range channels {
			if channel.Name == channelName {
				return channel.ID, nil
			}
		}
	}

	return "", fmt.Errorf("channel %v not found", channelName)
}

func getMessageText(ctx *ankh.ExecutionContext, chart *ankh.Chart, envOrContext string) (string, error) {

	// Override takes precedence
	if ctx.SlackMessageOverride != "" {
		return ctx.SlackMessageOverride, nil
	}

	// If format is set, use that
	format := ctx.AnkhConfig.Slack.Format
	if ctx.Mode == ankh.Rollback {
		format = ctx.AnkhConfig.Slack.RollbackFormat
	}

	if format != "" {
		message, err := util.NotificationString(format, chart, envOrContext)
		if err != nil {
			ctx.Logger.Infof("Unable to use format: '%v'. Will prompt for message", format)
		} else {
			return message, nil
		}
	}

	// Otherwise, prompt for message
	message, err := promptForMessageText(ctx, chart, envOrContext)
	if err != nil {
		ctx.Logger.Infof("Unable to prompt for message. Will use default message")
	}

	return message, nil
}

func getRollbackMessageText(ctx *ankh.ExecutionContext, chart *ankh.Chart, envOrContext string) (string, error) {

	var rollbackChart ankh.Chart
	rollbackChart = *chart
	rollbackChart.Name = strings.Split(ctx.RollbackChart, "-")[0]
	rollbackChart.Version = strings.Split(ctx.RollbackChart, "-")[1]
	rollbackChart.Tag = &ctx.RollbackTag
	rollbackChart.Path = ""

	// If format is set, use that
	format := "chart: %CHART_NAME% version:%CHART_VERSION% tag: %VERSION% environment: %TARGET%"

	if format != "" {
		message, err := util.RollbackInstructionNotificationString(format, &rollbackChart, envOrContext)
		if err != nil {
			ctx.Logger.Infof("Unable to use format: '%v'. Will prompt for message", format)
		} else {
			return message, nil
		}
	}

	// Otherwise, prompt for message
	message, err := promptForRollbackInstructionMessageText(&rollbackChart, envOrContext)
	if err != nil {
		ctx.Logger.Infof("Unable to prompt for rollback instructionmessage. Will use default message")
	}

	return message, nil
}

func promptForMessageText(ctx *ankh.ExecutionContext, chart *ankh.Chart, envOrContext string) (string, error) {
	currentUser, err := user.Current()
	if err != nil {
		return "", err
	}

	version := ""
	if chart.Tag != nil {
		version = *chart.Tag
	}

	defaultMessage := fmt.Sprintf("%s is releasing %s chart %s version %s to *%s*", currentUser.Username, chart.Name, chart.Version, version, envOrContext)
	if ctx.Mode == ankh.Rollback {
		defaultMessage = fmt.Sprintf("%s is rolling back %s in *%v*", currentUser, chart.Name, envOrContext)
	}

	message, err := util.PromptForInput(defaultMessage, "Slack Message")
	if err != nil {
		return defaultMessage, err
	}

	return message, nil
}

func promptForRollbackInstructionMessageText(chart *ankh.Chart, envOrContext string) (string, error) {

	version := ""
	if chart.Tag != nil {
		version = *chart.Tag
	}

	defaultMessage := fmt.Sprintf("Rollback Information %s chart %s version %s to *%s*", chart.Name, chart.Version, version, envOrContext)

	message, err := util.PromptForInput(defaultMessage, "Slack Message")
	if err != nil {
		return defaultMessage, err
	}

	return message, nil
}
