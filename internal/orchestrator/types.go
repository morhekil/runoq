package orchestrator

// IssueMetadata holds parsed metadata for an issue being processed.
type IssueMetadata struct {
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
