package issuerunner

// inputPayload is the JSON structure received as input.
type inputPayload struct {
	IssueNumber    int      `json:"issueNumber"`
	PRNumber       int      `json:"prNumber"`
	Worktree       string   `json:"worktree"`
	Branch         string   `json:"branch"`
	SpecPath       string   `json:"specPath"`
	Repo           string   `json:"repo"`
	MaxRounds      int      `json:"maxRounds"`
	MaxTokenBudget int      `json:"maxTokenBudget"`
	Guidelines     []string `json:"guidelines"`
	CriteriaCommit string   `json:"criteria_commit,omitempty"`
	// Resume fields
	Round             int    `json:"round,omitempty"`
	LogDir            string `json:"logDir,omitempty"`
	PreviousChecklist string `json:"previousChecklist,omitempty"`
	CumulativeTokens  int    `json:"cumulativeTokens,omitempty"`
}

// outputPayload is the JSON structure emitted to stdout.
type outputPayload struct {
	Status               string   `json:"status"`
	Round                int      `json:"round"`
	TotalRounds          int      `json:"total_rounds"`
	LogDir               string   `json:"logDir"`
	BaselineHash         string   `json:"baselineHash"`
	HeadHash             string   `json:"headHash"`
	CommitRange          string   `json:"commitRange"`
	ReviewLogPath        string   `json:"reviewLogPath,omitempty"`
	SpecRequirements     string   `json:"specRequirements"`
	ChangedFiles         []string `json:"changedFiles,omitempty"`
	RelatedFiles         []string `json:"relatedFiles,omitempty"`
	CumulativeTokens     int      `json:"cumulativeTokens"`
	VerificationPassed   bool     `json:"verificationPassed"`
	VerificationFailures []string `json:"verificationFailures,omitempty"`
	Caveats              []string `json:"caveats,omitempty"`
	Summary              string   `json:"summary,omitempty"`
}

// roundState tracks state across the round loop.
type roundState struct {
	round             int
	logDir            string
	baseline          string
	headHash          string
	cumulativeTokens  int
	previousChecklist string
	commitSubjects    []string
	threadID          string
}

// verifyResult holds the parsed output from verify.sh.
type verifyResult struct {
	ReviewAllowed bool     `json:"review_allowed"`
	Failures      []string `json:"failures"`
	Actual        struct {
		FilesChanged []string `json:"files_changed"`
		FilesAdded   []string `json:"files_added"`
		FilesDeleted []string `json:"files_deleted"`
	} `json:"actual"`
}
