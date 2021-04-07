package jira

import (
	"fmt"
	"log"
	"strings"

	jira "github.com/andygrunwald/go-jira"
	ankh "github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
)

func CreateJiraTicket(ctx *ankh.ExecutionContext, ankhFile *ankh.AnkhFile) error {
	base := ctx.AnkhConfig.Jira.BaseUrl
	if base == "" {
		return fmt.Errorf("No Jira base url provided. Unable to create ticket.")
	}

	queue := ctx.AnkhConfig.Jira.Queue
	if queue == "" {
		return fmt.Errorf("No Jira queue provided. Unable to create ticket.")
	}

	username, password, err := promptForAuth(ctx, 0)
	if err != nil {
		return fmt.Errorf("Unable to obtain authentication for JIRA")
	}
	tp := jira.BasicAuthTransport{
		Username: username,
		Password: password,
	}

	envOrContext := util.GetEnvironmentOrContext(ctx.Environment, ctx.Context)

	var summaries []string
	var descriptions []string
	for i := 0; i < len(ankhFile.Charts); i++ {
		chart := &ankhFile.Charts[i]
		summary, err := getSummary(ctx, chart, envOrContext)
		if err != nil {
			log.Fatal(err)
		} else {
			summaries = append(summaries, summary)
		}
		description, err := getDescription(ctx, chart, envOrContext)
		if err != nil {
			log.Fatal(err)
		} else {
			descriptions = append(descriptions, description)
		}
	}
	summaryText := strings.Join(summaries, ", ")
	descriptionText := strings.Join(descriptions, "\n")

	jiraClient, err := jira.NewClient(tp.Client(), base)
	if err != nil {
		log.Fatal(err)
	}
	i := jira.Issue{
		Fields: &jira.IssueFields{
			Assignee: &jira.User{
				Name: username,
			},
			Reporter: &jira.User{
				Name: username,
			},
			Summary: summaryText,
			// TODO: Should this be defined in ankh config?
			Type: jira.IssueType{
				Name: "Task",
			},
			Project: jira.Project{
				Key: queue,
			},
			Description: descriptionText,
		},
	}

	if !ctx.DryRun {
		issue, _, err := jiraClient.Issue.Create(&i)
		if err != nil {
			return fmt.Errorf("Unable to create JIRA issue. Error: %v", err)
		}

		// Auto-Close ticket
		autoClose := ctx.AnkhConfig.Jira.AutoClose
		// To get possible transitions use '[base-jira-url]/rest/api/2/issue/[jira-id]/transitions?expand=transitions.fields'
		if autoClose {
			if _, err = jiraClient.Issue.DoTransition(issue.Key, "111"); err != nil {
				return fmt.Errorf("Unable to close JIRA issue (%v)", issue.Key)
			}
		}
		ctx.Logger.Infof("Created JIRA Ticket: %v", issue.Key)
		return nil
	} else {
		ctx.Logger.Infof("--dry-run set, not creating JIRA Ticket for %v queue with summary '%v' and description '%v'", queue, summaryText, descriptionText)
		return nil
	}
}

func checkJiraAuth(username string, password string, ctx *ankh.ExecutionContext) error {
	base := ctx.AnkhConfig.Jira.BaseUrl
	tp := jira.BasicAuthTransport{
		Username: username,
		Password: password,
	}

	jiraClient, err := jira.NewClient(tp.Client(), base)
	if err != nil {
		return err
	}

	if _, _, err := jiraClient.User.Get(username); err != nil {
		return err
	}

	return nil

}

func promptForAuth(ctx *ankh.ExecutionContext, retryCount int) (string, string, error) {
	providedUsername, err := util.PromptForUsernameWithLabel("Jira Username: ")
	if err != nil {
		return "", "", err
	}

	providedPassword, err := util.PromptForPasswordWithLabel("Jira Password: ")
	if err != nil {
		return "", "", err
	}

	if err = checkJiraAuth(providedUsername, providedPassword, ctx); err != nil {
		if retryCount < 5 {
			retryCount += 1
			ctx.Logger.Error("Invalid JIRA Credentials. Please retry.")
			err = nil
			promptForAuth(ctx, retryCount)
		}
		return "", "", err
	}

	return providedUsername, providedPassword, nil
}

func getSummary(ctx *ankh.ExecutionContext, chart *ankh.Chart, envOrContext string) (string, error) {
	// If format is set, use that
	format := ctx.AnkhConfig.Jira.SummaryFormat
	if ctx.Mode == ankh.Rollback {
		format = ctx.AnkhConfig.Jira.RollbackSummaryFormat
	}

	if format != "" {
		message, err := util.NotificationString(format, chart, envOrContext)
		if err != nil {
			ctx.Logger.Infof("Unable to use format: '%v'. Will prompt for subject", format)
		} else {
			return message, nil
		}
	}

	// Otherwise, prompt for message
	message, err := promptForSummary(ctx, chart, envOrContext)
	if err != nil {
		ctx.Logger.Infof("Unable to prompt for subject. Will use default subject")
	}

	return message, nil
}

func getDescription(ctx *ankh.ExecutionContext, chart *ankh.Chart, envOrContext string) (string, error) {
	versionString := ""
	if chart.Tag != nil {
		versionString = *chart.Tag
	}

	// If format is set, use that
	format := ctx.AnkhConfig.Jira.DescriptionFormat
	if versionString == "rollback" {
		format = ctx.AnkhConfig.Jira.RollbackDescriptionFormat
	}

	if format != "" {
		message, err := util.NotificationString(format, chart, envOrContext)
		if err != nil {
			ctx.Logger.Infof("Unable to use format: '%v'. Will prompt for description", format)
		} else {
			return message, nil
		}
	}

	// Otherwise, prompt for message
	message, err := promptForDescription(ctx, chart, envOrContext)
	if err != nil {
		ctx.Logger.Infof("Unable to prompt for description. Will use default description")
	}

	return message, nil
}

func promptForSummary(ctx *ankh.ExecutionContext, chart *ankh.Chart, envOrContext string) (string, error) {
	version := ""
	if chart.Tag != nil {
		version = *chart.Tag
	}

	defaultSummary := fmt.Sprintf("Deployment of %s chart %s verson %s to *%s*", chart.Name, chart.Version, version, envOrContext)
	if ctx.Mode == ankh.Rollback {
		defaultSummary = fmt.Sprintf("Rollback of %s in *%s*", chart.Name, envOrContext)
	}

	message, err := util.PromptForInput(defaultSummary, "Jira Summary")
	if err != nil {
		return defaultSummary, err
	}

	return message, nil
}

func promptForDescription(ctx *ankh.ExecutionContext, chart *ankh.Chart, envOrContext string) (string, error) {
	version := ""
	if chart.Tag != nil {
		version = *chart.Tag
	}

	defaultSubject := fmt.Sprintf("Ticket to track the deployment of %s chart %s verson %s to *%s*", chart.Name, chart.Version, version, envOrContext)
	if ctx.Mode == ankh.Rollback {
		defaultSubject = fmt.Sprintf("Ticket to track the rollback of %s in *%s*", chart.Name, envOrContext)
	}

	message, err := util.PromptForInput(defaultSubject, "Jira Description")
	if err != nil {
		return defaultSubject, err
	}

	return message, nil
}
