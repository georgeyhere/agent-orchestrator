import { useState, type FormEvent } from "react";
import { Link } from "@tanstack/react-router";
import type { MissionDraft, TeamAction, TeamState } from "./TeamView";
import { Button } from "./ui/button";
import { Input } from "./ui/input";

export function TeamPlanning({
	projectId,
	state,
	busy,
	onAction,
}: {
	projectId: string;
	state: TeamState;
	busy: boolean;
	onAction: TeamAction;
}) {
	const [editing, setEditing] = useState<MissionDraft | "new" | null>(null);
	const planner = state.agents.find((agent) => agent.tier === "planner");
	const chiefs = state.agents.filter((agent) => agent.tier === "chief");
	const drafts = state.drafts ?? [];
	async function save(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		const values: Record<string, unknown> = Object.fromEntries(new FormData(event.currentTarget));
		if (!values.chief) values.chief = null;
		if (editing && editing !== "new") {
			values.draft = editing.id;
			values.revision = editing.revision;
		}
		if (await onAction(editing === "new" ? "draft-create" : "draft-update", values)) setEditing(null);
	}
	return (
		<section aria-label="Mission planning" className="max-w-4xl space-y-5">
			<div className="rounded-lg border p-5">
				<h2 className="font-medium">Plan missions with your project planner</h2>
				<p className="mt-2 text-sm text-muted-foreground">
					Discuss the goal in the planner's conversation. It saves the objective, acceptance criteria, proposed chief,
					and execution plan here. You review and release a draft when it is ready.
				</p>
				<div className="mt-4 flex flex-wrap items-center gap-3">
					{planner ? (
						<>
							<span className="text-sm">Planner {planner.id}</span>
							{planner.session_id ? (
								<Button asChild variant="outline">
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
							<Button onClick={() => setEditing("new")} disabled={busy}>
								Request mission planning
							</Button>
						</>
					) : (
						<p className="text-sm">
							Use Create planner above to add this project's planner. Its conversation and drafts stay with this
							project.
						</p>
					)}
				</div>
			</div>
			{editing && (
				<form
					key={editing === "new" ? "new" : editing.id}
					onSubmit={(event) => void save(event)}
					className="space-y-4 rounded-lg border p-5"
				>
					<h3 className="font-medium">
						{editing === "new" ? "New mission draft" : `Edit ${editing.id} at revision ${editing.revision}`}
					</h3>
					{editing === "new" && (
						<label className="block text-sm">
							Draft ID
							<Input name="draft" required className="mt-1" />
						</label>
					)}
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
					<label className="block text-sm">
						Proposed chief
						<select
							name="chief"
							defaultValue={editing === "new" ? "" : (editing.chief ?? "")}
							className="mt-1 block w-full rounded-md border bg-background p-2"
						>
							<option value="">Choose before release</option>
							{chiefs.map((chief) => (
								<option key={chief.id} value={chief.id}>
									{chief.id}
								</option>
							))}
						</select>
					</label>
					<p className="text-xs text-muted-foreground">
						Saving sends the draft to your planner. Execution starts only after you release a complete draft.
					</p>
					<div className="flex gap-2">
						<Button type="submit" disabled={busy}>
							Save draft
						</Button>
						<Button type="button" variant="ghost" disabled={busy} onClick={() => setEditing(null)}>
							Cancel
						</Button>
					</div>
				</form>
			)}
			{!drafts.length && (
				<p className="text-sm text-muted-foreground">
					No mission drafts yet. Start with a goal; refine the brief with your planner before releasing work.
				</p>
			)}
			{drafts.map((draft) => (
				<article key={draft.id} className="rounded-lg border p-5">
					<div className="flex justify-between gap-3">
						<h3 className="font-medium">{draft.id}</h3>
						<span className="text-sm">
							{draft.status} / revision {draft.revision}
						</span>
					</div>
					<p className="mt-1 text-xs text-muted-foreground">
						Planner {draft.planner} / proposed chief {draft.chief ?? "unassigned"}
					</p>
					<p className="mt-3 whitespace-pre-wrap text-sm">{draft.objective}</p>
					<p className="mt-3 text-xs text-muted-foreground">Acceptance criteria</p>
					<p className="whitespace-pre-wrap text-sm">{draft.criteria || "Still to be defined"}</p>
					<p className="mt-3 text-xs text-muted-foreground">Execution plan and constraints</p>
					<p className="whitespace-pre-wrap text-sm">{draft.plan || "Discuss the approach with your planner"}</p>
					{draft.status === "draft" ? (
						<div className="mt-4 flex flex-wrap gap-2">
							<Button variant="outline" disabled={busy} onClick={() => setEditing(draft)}>
								Edit draft {draft.id}
							</Button>
							<Button
								disabled={busy || !draft.chief || !draft.criteria.trim() || !draft.plan.trim()}
								onClick={() => void onAction("draft-release", { draft: draft.id, revision: draft.revision })}
							>
								Release mission {draft.id}
							</Button>
						</div>
					) : (
						<p className="mt-4 text-sm">
							Released as mission {draft.mission}. Its chief owns execution; the orchestrator gives final signoff.
						</p>
					)}
				</article>
			))}
		</section>
	);
}
