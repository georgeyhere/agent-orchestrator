import { useState } from "react";
import type { Agent } from "./TeamView";

export function MissionStaffingFields({
	agents,
	chief = "",
	workers = [],
	fixedChief = false,
}: {
	agents: Agent[];
	chief?: string;
	workers?: string[];
	fixedChief?: boolean;
}) {
	const [selectedChief, setChief] = useState(chief);
	const eligible = agents.filter((agent) => agent.tier === "worker" && agent.parent === selectedChief);
	return (
		<fieldset className="space-y-3 rounded-md border p-4">
			<legend className="px-1 text-sm font-medium">Mission staffing</legend>
			{fixedChief ? (
				<p className="text-sm">Responsible chief: {chief}</p>
			) : (
				<label className="block text-sm">
					Responsible chief
					<select
						name="chief"
						value={selectedChief}
						onChange={(event) => setChief(event.target.value)}
						className="mt-1 block w-full rounded-md border bg-background p-2"
					>
						<option value="">Choose before release</option>
						{agents
							.filter((agent) => agent.tier === "chief")
							.map((agent) => (
								<option key={agent.id} value={agent.id}>
									{agent.id}
								</option>
							))}
					</select>
				</label>
			)}
			<div key={selectedChief} className="space-y-2">
				<p className="text-sm font-medium">Assigned workers</p>
				{eligible.map((agent) => (
					<label key={agent.id} className="flex items-center gap-2 text-sm">
						<input
							type="checkbox"
							name="workers"
							value={agent.id}
							defaultChecked={agent.parent === chief && workers.includes(agent.id)}
						/>
						{agent.id}{" "}
						<span className="text-muted-foreground">
							({agent.role} / {agent.provider})
						</span>
					</label>
				))}
				{!eligible.length && (
					<p className="text-sm text-muted-foreground">Choose a chief with workers in the project org chart.</p>
				)}
			</div>
			<p className="text-xs text-muted-foreground">
				Release requires a chief and at least one named worker. The chief can assign tasks only to these workers.
			</p>
		</fieldset>
	);
}
