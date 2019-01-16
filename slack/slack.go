package slack

import (
	"fmt"

	ankh "github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
	"github.com/nlopes/slack"
)

const DEFAULT_ICON_URL = "https://github.com/appnexus/ankh/blob/master/ankh.png?raw=true"
const DEFAULT_USERNAME = "ankh"

// Send out a release message based on the chart, version and environment
// supplied by the user
func PingSlackChannel(ctx *ankh.ExecutionContext) error {

	// attempt the connection
	api := slack.New(ctx.AnkhConfig.Slack.Token)

	// get environment from env vs. context
	deploymentEnvironment := util.GetEnvironmentOrContext(ctx.Environment, ctx.Context)
	messageText := getMessageText(ctx.SlackDeploymentVersion, ctx.Chart, deploymentEnvironment, ctx.SlackMessageOverride)

	attachment := slack.Attachment{
		Color:   "good",
		Pretext: "A new release notification has been received",
		Text:    messageText,
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

	channelId, err := getSlackChannelIDByName(api, ctx.SlackChannel)
	if err != nil {
		return err
	}

	_, _, err = api.PostMessage(channelId, slack.MsgOptionAttachments(attachment), slack.MsgOptionPostMessageParameters(messageParams))

	return err
}

func getSlackChannelIDByName(api *slack.Client, channelName string) (string, error) {
	channels, err := api.GetChannels(true)
	if err != nil || channels == nil {
		return "", err
	}

	for _, channel := range channels {
		if channel.Name == channelName {
			return channel.ID, nil
		}
	}

	return "", fmt.Errorf("channel %v not found", channelName)
}

func getMessageText(version string, chart string, env string, messageOverride string) string {

	if messageOverride != "" {
		return messageOverride
	}

	if version == "rollback" {
		return fmt.Sprintf("*%v* is being ROLLED BACK to the previous version in *%v*", chart, env)
	}

	return fmt.Sprintf("*%v* _v.%v_ is being deployed to *%v*", chart, version, env)

}
