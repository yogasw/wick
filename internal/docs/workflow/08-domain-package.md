## 8. Domain package

```
internal/agents/workflow/
  workflow.go              # Workflow, Node, types
  nodes/
    classify.go            # classify node executor
    agent.go
    skill.go
    shell.go
    python.go
    http.go
    db_query.go
    transform.go
    branch.go
    parallel.go
    merge.go
  engine.go                # graph walker, state persister
  resolver.go              # template render, output ref resolution
  validator.go             # cycle detect, schema check
  trigger/
    router.go              # event matching, dedup, enqueue
    cron.go
    channel.go
    webhook.go
    manual.go
    schedule_at.go
  service.go               # CRUD on folder
  manager.go               # service + state + guard
  scaffold.go              # template per node type for MCP create
```

### Core types

```go
type NodeType string

const (
    NodeClassify    NodeType = "classify"
    NodeAgent       NodeType = "agent"        // skills accessed via skills:[] field
    NodeChannel     NodeType = "channel"      // symmetric: also a trigger type
    NodeConnector   NodeType = "connector"    // reuse internal/connectors/ ops
    NodeShell       NodeType = "shell"
    NodePython      NodeType = "python"
    NodeHTTP        NodeType = "http"
    NodeDBQuery     NodeType = "db_query"
    NodeTransform   NodeType = "transform"
    NodeBranch      NodeType = "branch"
    NodeParallel    NodeType = "parallel"
    NodeMerge       NodeType = "merge"
    NodeEnd         NodeType = "end"
    NodeDatasetGet     NodeType = "dataset_get"
    NodeDatasetExists  NodeType = "dataset_exists"
    NodeDatasetQuery   NodeType = "dataset_query"
    NodeDatasetInsert  NodeType = "dataset_insert"
    NodeDatasetUpsert  NodeType = "dataset_upsert"
    NodeDatasetDelete  NodeType = "dataset_delete"
    NodeDatasetCount   NodeType = "dataset_count"
)

type Workflow struct {
    ID             string // folder name; UUID for canvas-created, plain id for legacy
    Version        int
    Name           string // display title — freely renameable via /rename
    Description    string
    Enabled        bool
    MaxDurationSec int
    Triggers       []Trigger
    Queue          QueuePolicy
    Graph          Graph
    Env            map[string]string         // workflow-level env
    Secrets        map[string]string         // encrypted, decrypt runtime
    CreatedBy      string
    CreatedAt      time.Time
}

type Graph struct {
    Entry string            // default entry kalau trigger ga set entry_node
    Nodes []Node            // flat list, no embedded edges
    Edges []Edge            // separate edge list (n8n-style)
}

type Edge struct {
    From string             // source node ID
    To   string             // target node ID
    Case string             // optional: case label, only for classify/branch source
    Label string            // optional: display label di canvas (UI hint, no semantic)
}

type Node struct {
    ID          string
    Type        NodeType
    Label       string
    Description string                       // load-bearing untuk AI (§5)
    TimeoutSec  int
    Retry       *RetryPolicy
    OnFailure   string                       // halt | skip | fallback
    Fallback    string                       // node ID (kalau OnFailure=fallback)
    OutputSchema map[string]any              // JSON schema
    // NO Next/Cases here — di Graph.Edges

    // For parallel/merge node — declared per node
    Branches []string                        // parallel node: explicit branch list
    Inputs   []string                        // merge node: wait-for-all inputs
    Strategy string                          // merge strategy: object|array|first|last

    // type-specific fields, union-style
    Classify  *ClassifyNode
    Agent     *AgentNode
    Channel   *ChannelNode
    Connector *ConnectorNode
    Shell     *ShellNode
    Python    *PythonNode
    HTTP      *HTTPNode
    DBQuery   *DBQueryNode
    Transform *TransformNode
    Branch    *BranchNode
    Dataset   *DatasetNode                    // unified for dataset_get/exists/query/insert/upsert/delete/count
}

// Trigger ditambah entry_node (override Graph.Entry per-trigger)
type Trigger struct {
    Type      string                          // cron | channel | webhook | manual | schedule_at | error
    EntryNode string                          // override Graph.Entry kalau diset
    // ... type-specific fields per trigger type
}

// Service methods take `id` = folder name (UUID for new workflows).
// Param is still string-typed; legacy "slug" var names are aliases for id.
type Service interface {
    List() ([]Workflow, error)
    Load(id string) (Workflow, error)
    Create(id string, w Workflow, files map[string][]byte) error
    Update(id string, w Workflow, files map[string][]byte) error
    Delete(id string) error
    Toggle(id string, enabled bool) error
    Approve(id, userID string, override *Override) error
}
```

---

