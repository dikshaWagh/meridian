package balancer

import "math/rand"

type RandomPolicy struct{}

func (p *RandomPolicy) Name() string        { return "random" }
func (p *RandomPolicy) Description() string { return "pick a random healthy worker" }

func (p *RandomPolicy) Pick(model string, healthy []*Worker) *Worker {
	return healthy[rand.Intn(len(healthy))]
}
