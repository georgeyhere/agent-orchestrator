import { useState, type FormEvent } from "react";
import { Link } from "@tanstack/react-router";
import type { Agent, Mission, TeamAction, TeamState } from "./TeamView";
import { Button } from "./ui/button";

export function TeamOrgChart({
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
	const [worker, setWorker] = useState<Agent | null>(null);
	const [mission, setMission] = useState<Mission | null>(null);
	const chiefs = state.agents.filter((agent) => agent.tier === "chief");
	const supervisors = state.agents.filter((agent) => agent.tier === "chief" || agent.tier === "orchestrator");
	const roots = state.agents.filter((agent) => ["planner", "orchestrator"].includes(agent.tier));
	async function move(event: FormEvent<HTMLFormElement>) {
		event.preventDefault();
		const values = new FormData(event.currentTarget);
		const success = worker
			? await onAction("agent-reparent", { agent: worker.id, parent: values.get("supervisor"), revision: worker.revision })
			: mission &&
				(await onAction("mission-reassign", {
					mission: mission.id,
					supervisor: values.get("supervisor"),
					revision: mission.revision,
					move_workers: values.get("move_workers") === "on",
				}));
		if (success) {
			setWorker(null);
			setMission(null);
		}
	}
	function session(agent: Agent) {
		return (
			agent.session_id && (
				<Link
					className="text-xs underline"
					to="/projects/$projectId/sessions/$sessionId"
					params={{ projectId, sessionId: agent.session_id }}
				>
					Open {agent.id}
				</Link>
			)
		);
	}
	function tasks(agent: Agent) {
		return state.tasks
			.filter((task) => task.worker === agent.id && task.status !== "accepted")
			.map((task) => (
				<p key={task.id} className="mt-1 text-xs">
					Task {task.id} / mission {task.mission} / {task.status}
				</p>
			));
	}
	return (
		<section aria-label="Project org chart" className="space-y-5">
			<p className="max-w-3xl text-sm text-muted-foreground">
				This project's planner shapes missions with you. The orchestrator supervises chiefs, may supervise workers
				directly, and signs off missions. Each chief manages its workers. Assignments below are saved for this project.
			</p>
			{(worker || mission) && (
				<form
					key={worker?.id ?? mission?.id}
					onSubmit={(event) => void move(event)}
					className="max-w-2xl space-y-4 rounded-lg border p-5"
				>
					<h2 className="font-medium">{worker ? `Move worker ${worker.id}` : `Transfer mission ${mission?.id}`}</h2>
					<label className="block text-sm">
						New supervisor
						<select
							name="supervisor"
							required
							defaultValue=""
							className="mt-1 block w-full rounded-md border bg-background p-2"
						>
							<option value="" disabled>
								Select a chief or the orchestrator
							</option>
							{supervisors
								.filter((supervisor) => supervisor.id !== (worker?.parent ?? mission?.supervisor))
								.sort((left, right) => left.tier.localeCompare(right.tier))
								.map((supervisor) => (
									<option key={supervisor.id} value={supervisor.id}>
										{supervisor.id} ({supervisor.tier})
									</option>
								))}
						</select>
					</label>
					{mission && (
						<label className="flex gap-2 text-sm">
							<input type="checkbox" name="move_workers" />
							Move the mission's entire worker roster to the new supervisor
						</label>
					)}
					<p className="text-xs text-muted-foreground">
						An individual worker can move after its work and open mission staffing are cleared. Transfer a mission with its
						workers to preserve ongoing task and review ownership.
					</p>
					<div className="flex gap-2">
						<Button type="submit" disabled={busy}>
							Save assignment
						</Button>
						<Button
							type="button"
							variant="ghost"
							disabled={busy}
							onClick={() => {
								setWorker(null);
								setMission(null);
							}}
						>
							Cancel
						</Button>
					</div>
				</form>
			)}
			{!roots.length && (
				<p className="text-sm">
					Create the project's planner and orchestrator, then add chiefs and workers with Spawn agent.
				</p>
			)}
			<div className="grid gap-4 lg:grid-cols-2">
				{roots.map((root) => (
					<article key={root.id} aria-label={`${root.tier} ${root.id}`} className="rounded-lg border p-5">
						<p className="text-xs text-muted-foreground">Project {root.tier}</p>
						<h2 className="mt-1 font-medium">{root.id}</h2>
						<p className="mt-1 text-sm">Role {root.role}</p>
						{session(root)}
						{root.tier === "planner" && (
							<p className="mt-2 text-sm">
								{(state.drafts ?? []).filter((draft) => draft.planner === root.id && draft.status === "draft").length}{" "}
								mission drafts being planned
							</p>
						)}
						{root.tier === "orchestrator" && (
							<>
								<h3 className="mb-2 mt-4 text-sm font-medium">Direct missions</h3>
								{!state.missions.some((item) => item.supervisor === root.id) && (
									<p className="text-sm text-muted-foreground">No directly supervised missions</p>
								)}
								{state.missions
									.filter((item) => item.supervisor === root.id)
									.map((item) => (
										<div key={item.id} className="mb-2 rounded-md bg-muted/40 p-3">
											<p className="text-sm font-medium">
												{item.id} / {item.status}
											</p>
											<p className="mt-1 text-sm">{item.objective}</p>
											<p className="mt-1 text-xs">Staffed workers: {item.workers?.join(", ") || "Unassigned"}</p>
											{["active", "blocked"].includes(item.status) && supervisors.length > 1 && (
												<Button
													variant="ghost"
													size="sm"
													disabled={busy}
													onClick={() => {
														setWorker(null);
														setMission(item);
													}}
												>
													Transfer mission {item.id}
												</Button>
											)}
										</div>
									))}
								<h3 className="mb-2 mt-4 text-sm font-medium">Direct workers</h3>
								{!state.agents.some((agent) => agent.tier === "worker" && agent.parent === root.id) && (
									<p className="text-sm text-muted-foreground">No directly supervised workers</p>
								)}
								{state.agents
									.filter((agent) => agent.tier === "worker" && agent.parent === root.id)
									.map((agent) => (
										<div key={agent.id} className="mb-2 border-l-2 pl-3">
											<p className="text-sm font-medium">
												{agent.id} / {agent.role}
											</p>
											{session(agent)}
											{tasks(agent)}
											{supervisors.length > 1 && (
												<Button
													size="sm"
													variant="ghost"
													disabled={busy}
													onClick={() => {
														setMission(null);
														setWorker(agent);
													}}
												>
													Move worker {agent.id}
												</Button>
											)}
										</div>
									))}
							</>
						)}
					</article>
				))}
			</div>
			<div className="grid items-start gap-4 xl:grid-cols-2">
				{chiefs.map((chief) => (
					<article key={chief.id} aria-label={`Chief ${chief.id}`} className="rounded-lg border p-5">
						<p className="text-xs text-muted-foreground">Reports to {chief.parent}</p>
						<h2 className="mt-1 font-medium">Chief {chief.id}</h2>
						<p className="mt-1 text-sm">Role {chief.role}</p>
						{session(chief)}
						<h3 className="mb-2 mt-4 text-sm font-medium">Missions</h3>
						{!state.missions.some((item) => item.supervisor === chief.id) && (
							<p className="text-sm text-muted-foreground">No missions assigned</p>
						)}
						{state.missions
							.filter((item) => item.supervisor === chief.id)
							.map((item) => (
								<div key={item.id} className="mb-2 rounded-md bg-muted/40 p-3">
									<p className="text-sm font-medium">
										{item.id} / {item.status}
									</p>
									<p className="mt-1 text-sm">{item.objective}</p>
									<p className="mt-1 text-xs">Staffed workers: {item.workers?.join(", ") || "Unassigned"}</p>
									{["active", "blocked"].includes(item.status) && supervisors.length > 1 && (
										<Button
											variant="ghost"
											size="sm"
											disabled={busy}
											onClick={() => {
												setWorker(null);
												setMission(item);
											}}
										>
											Transfer mission {item.id}
										</Button>
									)}
								</div>
							))}
						<h3 className="mb-2 mt-4 text-sm font-medium">Workers</h3>
						{!state.agents.some((agent) => agent.tier === "worker" && agent.parent === chief.id) && (
							<p className="text-sm text-muted-foreground">No workers assigned</p>
						)}
						{state.agents
							.filter((agent) => agent.tier === "worker" && agent.parent === chief.id)
							.map((agent) => (
								<div key={agent.id} className="mb-2 border-l-2 pl-3">
									<p className="text-sm font-medium">
										{agent.id} / {agent.role}
									</p>
									{session(agent)}
									{tasks(agent)}
									{supervisors.length > 1 && (
										<Button
											size="sm"
											variant="ghost"
											disabled={busy}
											onClick={() => {
												setMission(null);
												setWorker(agent);
											}}
										>
											Move worker {agent.id}
										</Button>
									)}
								</div>
							))}
					</article>
				))}
			</div>
		</section>
	);
}
