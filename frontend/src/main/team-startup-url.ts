export function teamStartupUrl(defaultUrl: string, enabled: boolean): string {
	if (!enabled) return defaultUrl;
	const url = new URL(defaultUrl);
	url.hash = "/teams";
	return url.href;
}
