// Copyright (c) "Neo4j"
// Neo4j Sweden AB [http://neo4j.com]

package feedback

import (
	"github.com/mark3labs/mcp-go/mcp"
)

// feedbackMaxLength is the maximum length of a single feedback submission.
// Shared between the advertised schema (GiveFeedbackSpec) and the handler's
// own validation (handleGiveFeedback), since a client is not required to
// enforce the schema's maxLength constraint before sending a request.
const feedbackMaxLength = 300

// GiveFeedbackInput is the struct the handler binds incoming arguments into
// via request.BindArguments.
type GiveFeedbackInput struct {
	Feedback string `json:"feedback"`
}

// GiveFeedbackSpec declares the MCP tool schema for give-feedback.
func GiveFeedbackSpec() mcp.Tool {
	return mcp.NewTool("give-feedback",
		mcp.WithDescription(
			"Use this tool to give feedback about this MCP server itself — positive or negative. "+
				"This is for feedback on the server's tools, behaviour, documentation, or overall experience, "+
				"not for reporting Cypher errors or issues with the underlying Neo4j database. "+
				"Feedback is limited to 300 characters, so keep it concise and specific. "+
				"Use this when the user explicitly asks to leave feedback, or when you have a clear, "+
				"concrete observation about the server worth surfacing to its maintainers.",
		),
		mcp.WithString("feedback",
			mcp.Required(),
			mcp.MaxLength(feedbackMaxLength),
			mcp.Description("The feedback to submit, positive or negative. Maximum 300 characters. Required."),
		),
		mcp.WithTitleAnnotation("Give Feedback"),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithDestructiveHintAnnotation(false),
		mcp.WithIdempotentHintAnnotation(false),
		mcp.WithOpenWorldHintAnnotation(false),
	)
}
