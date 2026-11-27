import { useState } from "react";
import { Button } from "./ui/button";

// The desktop GUI is the Principal's control surface. Decisions taken here are
// recorded under the Principal's own identity, never as an agent; the agent
// whose role normally owns the decision is notified by the team runtime.
export function ReviewForm({
	kind,
	recordId,
	reviewer,
	principalName,
	acceptLabel,
	busy,
	onReview,
}: {
	kind: "task" | "mission";
	recordId: string;
	reviewer: string;
	principalName: string;
	acceptLabel: "Accept" | "Approve";
	busy: boolean;
	onReview: (approve: boolean, feedback: string) => void;
}) {
	const [feedback, setFeedback] = useState("");
	const ready = feedback.trim().length > 0 && !busy;
	return (
		<div className="mt-3 space-y-2 rounded-md border p-3">
			<p className="text-xs text-muted-foreground">
				{kind === "task"
					? `Task ${recordId} is waiting for ${reviewer}'s review.`
					: `Mission ${recordId} is waiting for final signoff by ${reviewer}.`}{" "}
				Deciding here records it as yours and notifies {reviewer}.
			</p>
			<label className="block text-sm">
				Review feedback
				<textarea
					aria-label={`Review feedback for ${recordId}`}
					value={feedback}
					onChange={(event) => setFeedback(event.target.value)}
					className="mt-1 block min-h-16 w-full rounded-md border bg-background p-2"
				/>
			</label>
			<div className="flex flex-wrap gap-2">
				<Button size="sm" disabled={!ready} onClick={() => onReview(true, feedback.trim())}>
					{acceptLabel} as {principalName}
				</Button>
				<Button size="sm" variant="outline" disabled={!ready} onClick={() => onReview(false, feedback.trim())}>
					Return as {principalName}
				</Button>
			</div>
		</div>
	);
}

export function MissionSubmitForm({
	missionId,
	reviewer,
	principalName,
	busy,
	onSubmit,
}: {
	missionId: string;
	reviewer: string;
	principalName: string;
	busy: boolean;
	onSubmit: (result: string, evidence: string) => void;
}) {
	const [result, setResult] = useState("");
	const [evidence, setEvidence] = useState("");
	const ready = result.trim().length > 0 && evidence.trim().length > 0 && !busy;
	return (
		<div className="mt-3 space-y-2 rounded-md border p-3">
			<p className="text-xs text-muted-foreground">
				All tasks are accepted. {reviewer} can record mission results for final signoff. Submitting here records the
				results as yours and notifies {reviewer}.
			</p>
			<label className="block text-sm">
				Mission result
				<textarea
					aria-label={`Mission result for ${missionId}`}
					value={result}
					onChange={(event) => setResult(event.target.value)}
					className="mt-1 block min-h-16 w-full rounded-md border bg-background p-2"
				/>
			</label>
			<label className="block text-sm">
				Verification evidence
				<textarea
					aria-label={`Verification evidence for ${missionId}`}
					value={evidence}
					onChange={(event) => setEvidence(event.target.value)}
					className="mt-1 block min-h-16 w-full rounded-md border bg-background p-2"
				/>
			</label>
			<div className="flex flex-wrap gap-2">
				<Button size="sm" disabled={!ready} onClick={() => onSubmit(result.trim(), evidence.trim())}>
					Submit results as {principalName}
				</Button>
			</div>
		</div>
	);
}
