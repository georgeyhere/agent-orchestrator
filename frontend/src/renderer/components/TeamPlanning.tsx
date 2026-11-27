import { useState, type FormEvent } from "react";
import { Link } from "@tanstack/react-router";
import type { MissionDraft, TeamAction, TeamState } from "./TeamView";
import { Button } from "./ui/button";
import { MissionStaffingFields } from "./MissionStaffingFields";
import { NameField, TeamDialog } from "./TeamDialog";
import { uniqueId } from "../lib/team-ids";

export function TeamPlanning({
	projectId,
	state,
	busy,
	error,
	onAction,
}: {
	projectId: string;
	state: TeamState;
	busy: boolean;
	error?: string;
	onAction: TeamAction;
}) {
	const [editing, setEditing] = useState<MissionDraft | "new" | null>(null);
	const [title, setTitle] = useState("");
	const planner = state.agents.find((agent) => agent.tier === "planner");
	const drafts = state.drafts ?? [];
	const draftId = uniqueId(title, [...drafts.map((draft) => draft.id), ...state.missions.map((mission) => mission.id)], "mission");
	async function save(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		const values: Record<string, unknown> = Object.fromEntries(new FormData(event.currentTarget));
		values.workers = new FormData(event.currentTarget).getAll("workers");
		delete values.title;
		if (editing === "new") values.draft = draftId;
		if (!values.supervisor) values.supervisor = null;
		if (editing && editing !== "new") {
			values.draft = editing.id;
			values.revision = editing.revision;
		}
		if (await onAction(editing === "new" ? "draft-create" : "draft-update", values)) setEditing(null);
	}
	return (
		<section aria-label="Mission planning" className="max-w-4xl space-y-5">
			<div>
				<div className="flex flex-wrap items-center gap-3">
					{planner ? (
						<>
							<span className="text-sm">Planner {planner.id}</span>
							{planner.session_id ? (
								<Button asChild variant="secondary">
									<Link
										to="/projects/$projectId/sessions/$sessionId"
										params={{ projectId, sessionId: planner.session_id }}
									>
										Open planner conversation
									</Link>
								</Button>
							) : (
								<span className="text-sm text-muted-foreground">
									{planner.launch_error ?? (planner.enabled ? "Planner launch queued" : "Planner is paused")}
								</span>
							)}
							<Button
								onClick={() => {
									setTitle("");
									setEditing("new");
								}}
								disabled={busy}
							>
								Request mission planning
							</Button>
						</>
					) : (
						<p className="text-sm">Use Create planner above to add this project's planner.</p>
					)}
				</div>
			</div>
			<TeamDialog
				open={editing !== null}
				title={editing === "new" ? "New mission draft" : `Edit draft ${editing?.id ?? ""}`}
				description="Saving sends the draft to your planner. Work starts only when you release it."
				error={error}
				onClose={() => setEditing(null)}
			>
			{editing && (
				<form
					key={editing === "new" ? "new" : editing.id}
					onSubmit={(event) => void save(event)}
					className="space-y-4"
				>
					{editing === "new" && <NameField label="Mission title" value={title} id={draftId} onChange={setTitle} />}
					{(
						[
							["objective", "Mission objective"],
							["criteria", "Acceptance criteria"],
							["plan", "Execution plan and constraints"],
						] as const
					).map(([name, label]) => (
						<label key={name} className="block text-sm">
							{label}
							<textarea
								name={name}
								required={name === "objective"}
								defaultValue={editing === "new" ? "" : editing[name]}
								className="mt-1 block min-h-24 w-full rounded-md border bg-background p-2"
							/>
						</label>
					))}
					<MissionStaffingFields agents={state.agents} supervisor={editing === "new" ? "" : editing.supervisor ?? ""} workers={editing === "new" ? [] : editing.workers} />
					<div className="flex justify-end gap-2">
						<Button type="button" variant="ghost" disabled={busy} onClick={() => setEditing(null)}>
							Cancel
						</Button>
						<Button type="submit" disabled={busy}>
							Save draft
						</Button>
					</div>
				</form>
			)}
			</TeamDialog>
			{!drafts.length && (
				<p className="text-sm text-muted-foreground">No mission drafts yet.</p>
			)}
			{drafts.map((draft) => (
				<article key={draft.id} className="rounded-lg border p-5">
					<div className="flex justify-between gap-3">
						<h3 className="font-medium">{draft.id}</h3>
						<span className="text-sm">{draft.status}</span>
					</div>
					<p className="mt-1 text-xs text-muted-foreground">
						Planner {draft.planner} / proposed supervisor {draft.supervisor ?? "unassigned"}
					</p>
					<p className="mt-1 text-sm">Assigned workers: {draft.workers?.join(", ") || "Unassigned"}</p>
					<p className="mt-3 whitespace-pre-wrap text-sm">{draft.objective}</p>
					<p className="mt-3 text-xs text-muted-foreground">Acceptance criteria</p>
					<p className="whitespace-pre-wrap text-sm">{draft.criteria || "Still to be defined"}</p>
					<p className="mt-3 text-xs text-muted-foreground">Execution plan and constraints</p>
					<p className="whitespace-pre-wrap text-sm">{draft.plan || "Discuss the approach with your planner"}</p>
					{draft.status === "draft" ? (
						<div className="mt-4 flex flex-wrap gap-2">
							<Button variant="secondary" disabled={busy} onClick={() => setEditing(draft)}>
								Edit draft {draft.id}
							</Button>
							<Button
								disabled={busy || !draft.supervisor || !draft.workers?.length || !draft.criteria.trim() || !draft.plan.trim()}
								onClick={() => void onAction("draft-release", { draft: draft.id, revision: draft.revision })}
							>
								Release mission {draft.id}
							</Button>
						</div>
					) : (
						<p className="mt-4 text-sm">
							Released as mission {draft.mission}. {state.agents.find((agent) => agent.id === draft.supervisor)?.tier === "orchestrator"
								? "The orchestrator owns execution and final signoff."
								: "Its chief owns execution; the orchestrator gives final signoff."}
						</p>
					)}
				</article>
			))}
		</section>
	);
}
