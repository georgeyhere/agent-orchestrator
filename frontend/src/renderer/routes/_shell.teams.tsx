import { createFileRoute } from "@tanstack/react-router";
import { TeamView } from "../components/TeamView";

export type TeamsSearch = {
	project?: string;
	tab?: "missions" | "planning" | "agents" | "org";
};

const TABS = new Set(["missions", "planning", "agents", "org"]);

export const Route = createFileRoute("/_shell/teams")({
	component: TeamView,
	validateSearch: (search: Record<string, unknown>): TeamsSearch => ({
		...(typeof search.project === "string" && search.project ? { project: search.project } : {}),
		...(typeof search.tab === "string" && TABS.has(search.tab) ? { tab: search.tab as TeamsSearch["tab"] } : {}),
	}),
});
