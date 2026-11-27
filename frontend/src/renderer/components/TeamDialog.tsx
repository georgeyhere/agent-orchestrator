import type { ReactNode } from "react";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "./ui/dialog";
import { Input } from "./ui/input";

/**
 * One modal for every Project teams editor, so an open form never pushes the
 * page content around. The action error renders inside the dialog: a modal
 * hides the page beneath it from assistive tech, and the error belongs to the
 * form that caused it.
 */
export function TeamDialog({
	open,
	title,
	description,
	error,
	onClose,
	children,
}: {
	open: boolean;
	title: string;
	description?: string;
	error?: string;
	onClose: () => void;
	children: ReactNode;
}) {
	return (
		<Dialog open={open} onOpenChange={(next) => !next && onClose()}>
			<DialogContent className="max-h-[calc(100svh-var(--space-8))] max-w-xl overflow-y-auto">
				<DialogTitle className="text-subtitle">{title}</DialogTitle>
				{description ? <DialogDescription>{description}</DialogDescription> : <DialogDescription className="sr-only">{title}</DialogDescription>}
				{error && (
					<p role="alert" className="text-sm text-destructive">
						{error}
					</p>
				)}
				{children}
			</DialogContent>
		</Dialog>
	);
}

/** Name field that shows the ID the backend will store. */
export function NameField({
	label,
	value,
	id,
	onChange,
}: {
	label: string;
	value: string;
	id: string;
	onChange: (value: string) => void;
}) {
	return (
		<div>
			<label className="block text-sm">
				{label}
				<Input name="title" required value={value} onChange={(event) => onChange(event.target.value)} className="mt-1" />
			</label>
			{id && <p className="mt-1 font-mono text-xs text-muted-foreground">{id}</p>}
		</div>
	);
}
