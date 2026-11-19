package balancer

// WorkerSpec describes one llama-server instance the gateway supervises.
type WorkerSpec struct {
	ID    string
	Model string
	Port  int
	File  string
}
