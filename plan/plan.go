package plan

import (
	"github.com/appnexus/ankh/context"
)

type PlanStage struct {
	Stage Stage
	Opts StageOpts
}
type Plan struct {
	PlanStages []PlanStage
}

type Stage interface {
	Execute(ctx *ankh.ExecutionContext, input *string, namespace string, wildCardLabels []string) (string, error)
}

type StageOpts struct {
	PreExecute func() bool
	OnFailure func() bool
	PassThroughInput bool
}

func Execute(ctx *ankh.ExecutionContext, namespace string, wildCardLabels []string, plan *Plan) (string, error) {
	input := ""
	for _, ps := range plan.PlanStages {
		if ps.Opts.PreExecute != nil {
			ok := ps.Opts.PreExecute()
			if !ok {
				// TODO this is sloppy and bad
				if !ps.Opts.PassThroughInput {
					input = ""
				}
				return input, nil
			}
		}

		out, err := ps.Stage.Execute(ctx, &input, namespace, wildCardLabels)
		if err != nil {
			if ps.Opts.OnFailure != nil {
				ok := ps.Opts.OnFailure()
				if !ok {
					return "", err
				}
			} else {
				return "", err
			}
		}

		if !ps.Opts.PassThroughInput {
			input = out
		}
	}

	return input, nil
}
