package jira

import (
	"fmt"
	"log"
	"strings"

	jira "github.com/andygrunwald/go-jira"
	ankh "github.com/appnexus/ankh/context"
	"github.com/appnexus/ankh/util"
)

func CreateJiraTicket(ctx *ankh.ExecutionContext) error {
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

	deploymentEnvironment := util.GetEnvironmentOrContext(ctx.Environment, ctx.Context)
	summary, err := getSummary(ctx, deploymentEnvironment)
	description, err := getDescription(ctx, deploymentEnvironment)

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
			Summary: summary,
			// TODO: Should this be defined in ankh config?
			Type: jira.IssueType{
				Name: "Task",
			},
			Project: jira.Project{
				Key: queue,
			},
			Description: description,
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
		ctx.Logger.Infof("--dry-run set, not creating JIRA Ticket for %v queue with summary '%v' and description '%v'", queue, summary, description)
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

func getSummary(ctx *ankh.ExecutionContext, env string) (string, error) {
	// If format is set, use that
	format := ctx.AnkhConfig.Jira.SummaryFormat
	if ctx.Mode == ankh.Rollback {
		format = ctx.AnkhConfig.Jira.RollbackSummaryFormat
	}

	// Allow for multiple charts
	chart := strings.Join(ctx.DeploymentChart, ", ")

	if format != "" {
		message, err := util.ReplaceFormatVariables(format, chart, ctx.DeploymentTag, env)
		if err != nil {
			ctx.Logger.Infof("Unable to use format: '%v'. Will prompt for subject", format)
		} else {
			return message, nil
		}
	}

	// Otherwise, prompt for message
	message, err := promptForSummary(chart, ctx.DeploymentTag, env)
	if err != nil {
		ctx.Logger.Infof("Unable to prompt for subject. Will use default subject")
	}

	return message, nil
}

func getDescription(ctx *ankh.ExecutionContext, env string) (string, error) {
	// If format is set, use that
	format := ctx.AnkhConfig.Jira.DescriptionFormat
	if ctx.DeploymentTag == "rollback" {
		format = ctx.AnkhConfig.Jira.RollbackDescriptionFormat
	}

	// Allow for multiple charts
	chart := strings.Join(ctx.DeploymentChart, ", ")

	if format != "" {
		message, err := util.ReplaceFormatVariables(format, chart, ctx.DeploymentTag, env)
		if err != nil {
			ctx.Logger.Infof("Unable to use format: '%v'. Will prompt for description", format)
		} else {
			return message, nil
		}
	}

	// Otherwise, prompt for message
	message, err := promptForDescription(chart, ctx.DeploymentTag, env)
	if err != nil {
		ctx.Logger.Infof("Unable to prompt for description. Will use default description")
	}

	return message, nil
}

func promptForSummary(chart string, version string, env string) (string, error) {
	defaultSubject := fmt.Sprintf("Deployment of %v verson:%v to *%v*", chart, version, env)
	if env == "rollback" {
		defaultSubject = fmt.Sprintf("Rollback of %v in *%v*", chart, env)
	}

	message, err := util.PromptForInput(defaultSubject, "Jira Summary")
	if err != nil {
		return defaultSubject, err
	}

	return message, nil
}

func promptForDescription(chart string, version string, env string) (string, error) {
	defaultSubject := fmt.Sprintf("Ticket to track the deployment of %v verson:%v to *%v*", chart, version, env)
	if env == "rollback" {
		defaultSubject = fmt.Sprintf("Ticket to track the rollback of %v in *%v*", chart, env)
	}

	message, err := util.PromptForInput(defaultSubject, "Jira Description")
	if err != nil {
		return defaultSubject, err
	}

	return message, nil
}
