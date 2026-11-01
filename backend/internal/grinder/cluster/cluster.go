package cluster

import (
	"context"
	"sync"

	log "github.com/sirupsen/logrus"

	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/commands"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/config"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/events"
	"github.com/zZzWhitePanda/DMF/backend/internal/grinder/instance"
)

// Runner owns the lifecycle of all instances inside a single cluster.
type Runner struct {
	cfg       *config.Config
	cluster   *config.Cluster
	instances []*instance.Instance
	wg        sync.WaitGroup
}

// New wires up master + secondary instances for a cluster.
func New(cl *config.Cluster, cfg *config.Config, bus *events.Bus) (*Runner, error) {
	r := &Runner{cfg: cfg, cluster: cl}

	masterLog := log.WithFields(log.Fields{"cluster": cl.Name, "acct": cl.Master.Name, "role": "master"})
	master, err := instance.New(cfg, cl, &cl.Master, true, masterLog, bus)
	if err != nil {
		return nil, err
	}
	master.SetCommands(commands.Build(cfg.Features.Commands))
	r.instances = append(r.instances, master)

	for i := range cl.Secondary {
		secLog := log.WithFields(log.Fields{"cluster": cl.Name, "acct": cl.Secondary[i].Name, "role": "secondary"})
		sec, err := instance.New(cfg, cl, &cl.Secondary[i], false, secLog, bus)
		if err != nil {
			return nil, err
		}
		sec.MasterUser = cl.Master.Name
		sec.SetCommands(commands.Build(cfg.Features.Commands))
		r.instances = append(r.instances, sec)
	}
	return r, nil
}

// Run starts all instances and returns immediately. Use Wait to block.
func (r *Runner) Run(ctx context.Context) {
	for _, inst := range r.instances {
		r.wg.Add(1)
		go func(i *instance.Instance) {
			defer r.wg.Done()
			i.Log.Info("instance starting")
			i.Run(ctx)
			i.Log.Info("instance stopped")
		}(inst)
	}
}

// Wait blocks until all instances have stopped.
func (r *Runner) Wait() {
	r.wg.Wait()
}
