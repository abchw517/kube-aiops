package k8sgpt

type Failure struct {
	Text string `json:"text,omitempty"`
}

type TargetReference struct {
	APIVersion string `json:"apiVersion,omitempty"`
	Kind       string `json:"kind,omitempty"`
	Namespace  string `json:"namespace,omitempty"`
	Name       string `json:"name,omitempty"`
}

type ResultSpec struct {
	Backend      string           `json:"backend"`
	Kind         string           `json:"kind"`
	Name         string           `json:"name"`
	Error        []Failure        `json:"error"`
	Details      string           `json:"details"`
	ParentObject string           `json:"parentObject"`
	TargetRef    *TargetReference `json:"targetRef,omitempty"`
}

type Metadata struct {
	Name              string            `json:"name"`
	Namespace         string            `json:"namespace"`
	CreationTimestamp string            `json:"creationTimestamp"`
	Labels            map[string]string `json:"labels"`
}

type Result struct {
	Metadata Metadata   `json:"metadata"`
	Spec     ResultSpec `json:"spec"`
}

type ResultList struct {
	Metadata struct {
		Continue string `json:"continue"`
	} `json:"metadata"`
	Items []Result `json:"items"`
}
