import { useState } from "react";
import type { Agent } from "./TeamView";

export function MissionStaffingFields({
	agents,
	supervisor = "",
	workers = [],
	fixedSupervisor = false,
}: {
	agents: Agent[];
	supervisor?: string;
	workers?: string[];
	fixedSupervisor?: boolean;
}) {
	const [selectedSupervisor, setSupervisor] = useState(supervisor);
	const eligible = agents.filter((agent) => agent.tier === "worker" && agent.parent === selectedSupervisor);
	return (
		<fieldset className="space-y-3 rounded-md border p-4">
			<legend className="px-1 text-sm font-medium">Mission staffing</legend>
			{fixedSupervisor ? (
				<p className="text-sm">Responsible supervisor: {supervisor}</p>
			) : (
				<label className="block text-sm">
					Responsible supervisor
					<select
						name="supervisor"
						value={selectedSupervisor}
						onChange={(event) => setSupervisor(event.target.value)}
						className="mt-1 block w-full rounded-md border bg-background p-2"
					>
						<option value="">Choose before release</option>
						{agents
							.filter((agent) => agent.tier === "chief" || agent.tier === "orchestrator")
							.sort((left, right) => left.tier.localeCompare(right.tier))
							.map((agent) => (
								<option key={agent.id} value={agent.id}>
									{agent.id} ({agent.tier})
								</option>
							))}
					</select>
				</label>
			)}
			<div key={selectedSupervisor} className="space-y-2">
				<p className="text-sm font-medium">Assigned workers</p>
				{eligible.map((agent) => (
					<label key={agent.id} className="flex items-center gap-2 text-sm">
						<input
							type="checkbox"
							name="workers"
							value={agent.id}
							defaultChecked={agent.parent === supervisor && workers.includes(agent.id)}
						/>
						{agent.id}{" "}
						<span className="text-muted-foreground">
							({agent.role} / {agent.provider})
						</span>
					</label>
				))}
				{!eligible.length && (
					<p className="text-sm text-muted-foreground">Choose a supervisor with workers in the project org chart.</p>
				)}
			</div>
			<p className="text-xs text-muted-foreground">
				Release requires a supervisor and at least one named worker. The supervisor can assign tasks only to these workers.
			</p>
		</fieldset>
	);
}
