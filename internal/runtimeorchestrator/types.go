package runtimeorchestrator

type issueMetadata struct {
	Number              int
	Title               string
	Body                string
	URL                 string
	EstimatedComplexity string
	ComplexityRationale *string
	Type                string
}

type issueView struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Body   string `json:"body"`
	URL    string `json:"url"`
}

type queueConfig struct {
	Labels struct {
		Ready string `json:"ready"`
	} `json:"labels"`
	Identity struct {
		Handle string `json:"handle"`
	} `json:"identity"`
	AutoMerge struct {
		Enabled       bool   `json:"enabled"`
		MaxComplexity string `json:"maxComplexity"`
	} `json:"autoMerge"`
	Reviewers      []string `json:"reviewers"`
	MaxRounds      int      `json:"maxRounds"`
	MaxTokenBudget int      `json:"maxTokenBudget"`
}

type eligibilityResult struct {
	Allowed bool     `json:"allowed"`
	Issue   int      `json:"issue"`
	Branch  string   `json:"branch"`
	Reasons []string `json:"reasons"`
}

type worktreeCreateResult struct {
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
}

type prCreateResult struct {
	Number any `json:"number"`
}

type queueSelectionResult struct {
	Issue   *queueSelectionIssue  `json:"issue"`
	Skipped []queueSelectionIssue `json:"skipped"`
}

type queueSelectionIssue struct {
	Number         int      `json:"number"`
	Title          string   `json:"title"`
	BlockedReasons []string `json:"blocked_reasons"`
}

type queueListedIssue struct {
	Number int    `json:"number"`
	Title  string `json:"title"`
	Type   string `json:"type"`
}

type epicStatusResult struct {
	AllDone bool  `json:"all_done"`
	Pending []int `json:"pending"`
}

type verifyIntegrateResult struct {
	OK       bool     `json:"ok"`
	Failures []string `json:"failures"`
}

type issueRunnerResult struct {
	Status               string   `json:"status"`
	LogDir               string   `json:"logDir"`
	BaselineHash         string   `json:"baselineHash"`
	HeadHash             string   `json:"headHash"`
	CommitRange          string   `json:"commitRange"`
	ReviewLogPath        string   `json:"reviewLogPath"`
	SpecRequirements     string   `json:"specRequirements"`
	ChangedFiles         []string `json:"changedFiles"`
	RelatedFiles         []string `json:"relatedFiles"`
	CumulativeTokens     int      `json:"cumulativeTokens"`
	VerificationPassed   bool     `json:"verificationPassed"`
	VerificationFailures []string `json:"verificationFailures"`
	Caveats              []string `json:"caveats"`
	Summary              string   `json:"summary"`
}
