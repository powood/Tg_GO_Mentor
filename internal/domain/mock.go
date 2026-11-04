package domain



// MockVerdictType — статус решения по кандидату
type MockVerdictType string

const (
	VerdictStrongHire MockVerdictType = "Strong Hire"
	VerdictHire       MockVerdictType = "Hire"
	VerdictWeakHire   MockVerdictType = "Weak Hire"
	VerdictNoHire     MockVerdictType = "No Hire"
)

// MockQuestionReport — протокол одного вопроса внутри мок-интервью
type MockQuestionReport struct {
	QuestionID     int        `json:"question_id"`
	QuestionText   string     `json:"question_text"`
	TopicTitle     string     `json:"topic_title"`
	FirstAnswer    string     `json:"first_answer"`
	FollowUpQ      string     `json:"follow_up_q,omitempty"`
	FollowUpAnswer string     `json:"follow_up_answer,omitempty"`
	Eval           Evaluation `json:"eval"`
}

// MockRoundState — состояние активного мок-интервью в FSM
type MockRoundState struct {
	Grade          string               `json:"grade"`
	TopicIDs       []string             `json:"topic_ids,omitempty"`
	CurrentIndex   int                  `json:"current_index"` // 0-based: 0..TotalQuestions-1
	TotalQuestions int                  `json:"total_questions"`
	InFollowUp     bool                 `json:"in_follow_up"`
	FollowUpQ      string               `json:"follow_up_q,omitempty"`
	FirstAnswer    string               `json:"first_answer,omitempty"`
	History        []MockQuestionReport `json:"history"`
}

// MockVerdict — итоговый отчет и вердикт собеседования
type MockVerdict struct {
	Verdict         MockVerdictType `json:"verdict"`
	ConfirmedGrade  string          `json:"confirmed_grade"`
	AverageScore    float64         `json:"average_score"`
	Summary         string          `json:"summary"`
	Strengths       []string        `json:"strengths"`
	Weaknesses      []string        `json:"weaknesses"`
	Recommendations string          `json:"recommendations"`
}

func (v *MockVerdict) Normalize() {
	if v.Verdict == "" {
		if v.AverageScore >= 8.5 {
			v.Verdict = VerdictStrongHire
		} else if v.AverageScore >= 6.5 {
			v.Verdict = VerdictHire
		} else if v.AverageScore >= 4.5 {
			v.Verdict = VerdictWeakHire
		} else {
			v.Verdict = VerdictNoHire
		}
	}
	v.Summary = truncateRunes(cleanText(v.Summary), 1000)
	v.Recommendations = truncateRunes(cleanText(v.Recommendations), 1000)
	for i, s := range v.Strengths {
		v.Strengths[i] = truncateRunes(cleanText(s), 250)
	}
	for i, w := range v.Weaknesses {
		v.Weaknesses[i] = truncateRunes(cleanText(w), 250)
	}
}

// MockEvalResponse — промежуточный ответ ИИ на первый ответ кандидата
type MockEvalResponse struct {
	NeedsFollowUp    bool        `json:"needs_follow_up"`
	FollowUpQuestion string      `json:"follow_up_question,omitempty"`
	Evaluation       *Evaluation `json:"evaluation,omitempty"`
}

// MockAnswerResult — результат обработки ответа хэндлером
type MockAnswerResult struct {
	IsFollowUp   bool         `json:"is_follow_up"`
	FollowUpQ    string       `json:"follow_up_q,omitempty"`
	Eval         *Evaluation  `json:"eval,omitempty"`
	IsFinished   bool         `json:"is_finished"`
	FinalVerdict *MockVerdict `json:"final_verdict,omitempty"`
	NextQuestion *Question    `json:"next_question,omitempty"`
	QuestionNum  int          `json:"question_num"`
	TotalNum     int          `json:"total_num"`
}
