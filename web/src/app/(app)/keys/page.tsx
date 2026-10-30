"use client";

import { useQueryClient } from "@tanstack/react-query";
import { KeyRound, Plus } from "lucide-react";
import { useState, type FormEvent } from "react";
import { toast } from "sonner";

import { CopyField } from "@/components/qanat/copy-field";
import { EmptyState, PageHeader, Tag } from "@/components/qanat/page";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api, errorMessage, type Schemas } from "@/lib/api/client";
import { settingsSnippet } from "@/lib/snippet";

const dateFmt = new Intl.DateTimeFormat("en-US", { dateStyle: "medium" });
const when = (v: string | null, empty = "—") => (v ? dateFmt.format(new Date(v)) : empty);

function keyStatus(k: Schemas["Key"]): "active" | "revoked" | "expired" {
  if (k.revoked_at) return "revoked";
  if (k.expires_at && new Date(k.expires_at) < new Date()) return "expired";
  return "active";
}

function CreateKeyDialog({ open, onOpenChange, onCreated }: { open: boolean; onOpenChange: (o: boolean) => void; onCreated: (k: Schemas["CreatedKey"]) => void }) {
  const [expiry, setExpiry] = useState("never");
  const [error, setError] = useState<string | null>(null);
  const create = api.useMutation("post", "/api/me/keys");

  const submit = (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault();
    const name = String(new FormData(e.currentTarget).get("name") ?? "");
    setError(null);
    create.mutate(
      { body: { name, expires_in_days: expiry === "never" ? null : Number(expiry) } },
      { onSuccess: (k) => onCreated(k), onError: (err) => setError(errorMessage(err)) },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <form onSubmit={submit} className="flex flex-col gap-4">
          <DialogHeader>
            <DialogTitle>Create key</DialogTitle>
            <DialogDescription>Use one key per machine, so you can revoke one without touching the others.</DialogDescription>
          </DialogHeader>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="key-name">Name</Label>
            <Input id="key-name" name="name" placeholder="Work laptop" maxLength={64} required autoFocus />
          </div>
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="key-expiry">Expires</Label>
            <Select value={expiry} onValueChange={setExpiry}>
              <SelectTrigger id="key-expiry" className="w-full">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="never">Never</SelectItem>
                <SelectItem value="30">In 30 days</SelectItem>
                <SelectItem value="90">In 90 days</SelectItem>
                <SelectItem value="365">In a year</SelectItem>
              </SelectContent>
            </Select>
          </div>
          {error && (
            <p role="alert" className="text-destructive-text">
              {error}
            </p>
          )}
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              Cancel
            </Button>
            <Button type="submit" disabled={create.isPending}>
              Create key
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

/** The key's one and only reveal (PRD §13.2). Closing it loses the key. */
function RevealDialog({ created, onClose }: { created: Schemas["CreatedKey"] | null; onClose: () => void }) {
  const setup = api.useQuery("get", "/api/me/setup");
  return (
    <Dialog open={!!created} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="sm:max-w-2xl">
        <DialogHeader>
          <DialogTitle>Copy your key now</DialogTitle>
          <DialogDescription>This is the only time it&apos;s shown. If you lose it, revoke it and create a new one.</DialogDescription>
        </DialogHeader>
        {created && (
          <div className="flex min-w-0 flex-col gap-4">
            <CopyField label="API key" value={created.key} />
            {setup.data && (
              <div className="flex min-w-0 flex-col gap-1.5">
                <p className="text-muted-foreground">Or paste this into ~/.claude/settings.json:</p>
                <CopyField label="settings.json" value={settingsSnippet(setup.data.gateway_url, created.key)} multiline />
              </div>
            )}
          </div>
        )}
        <DialogFooter>
          <Button onClick={onClose}>Done</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export default function KeysPage() {
  const qc = useQueryClient();
  const keys = api.useQuery("get", "/api/me/keys");
  const revoke = api.useMutation("delete", "/api/me/keys/{id}");
  const [creating, setCreating] = useState(false);
  const [created, setCreated] = useState<Schemas["CreatedKey"] | null>(null);
  const [revoking, setRevoking] = useState<Schemas["Key"] | null>(null);

  const refresh = () => qc.invalidateQueries({ queryKey: ["get", "/api/me/keys"] });
  const createButton = (
    <Button onClick={() => setCreating(true)}>
      <Plus /> Create key
    </Button>
  );

  return (
    <>
      <PageHeader title="Keys" icon={KeyRound} description="Personal API keys for Claude Code. Only a fingerprint is stored." actions={createButton} />

      {keys.isPending ? (
        <Skeleton className="h-32 w-full" />
      ) : !keys.data?.length ? (
        <EmptyState title="No keys yet">Create a key to connect Claude Code.</EmptyState>
      ) : (
        <div className="overflow-x-auto">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Name</TableHead>
                <TableHead>Key</TableHead>
                <TableHead>Created</TableHead>
                <TableHead>Last used</TableHead>
                <TableHead>Expires</TableHead>
                <TableHead>Status</TableHead>
                <TableHead>
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {keys.data.map((k) => {
                const status = keyStatus(k);
                return (
                  <TableRow key={k.id} className={status === "active" ? undefined : "text-muted-foreground"}>
                    <TableCell className="font-semibold">{k.name}</TableCell>
                    <TableCell className="font-mono">{k.prefix}…</TableCell>
                    <TableCell data-volatile>{when(k.created_at)}</TableCell>
                    <TableCell data-volatile>{k.last_used_at ? `${when(k.last_used_at)}${k.last_ip ? ` · ${k.last_ip}` : ""}` : "Never"}</TableCell>
                    <TableCell data-volatile>{when(k.expires_at, "Never")}</TableCell>
                    <TableCell>
                      <Tag tone={status === "active" ? "brand" : "neutral"}>{status === "active" ? "Active" : status === "revoked" ? "Revoked" : "Expired"}</Tag>
                    </TableCell>
                    <TableCell className="text-end">
                      {status === "active" && (
                        <Button variant="ghost" size="sm" onClick={() => setRevoking(k)}>
                          Revoke
                        </Button>
                      )}
                    </TableCell>
                  </TableRow>
                );
              })}
            </TableBody>
          </Table>
        </div>
      )}

      <CreateKeyDialog
        open={creating}
        onOpenChange={setCreating}
        onCreated={(k) => {
          setCreating(false);
          setCreated(k);
          refresh();
          toast("Key created");
        }}
      />
      <RevealDialog created={created} onClose={() => setCreated(null)} />

      <AlertDialog open={!!revoking} onOpenChange={(o) => !o && setRevoking(null)}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Revoke “{revoking?.name}”?</AlertDialogTitle>
            <AlertDialogDescription>Claude Code using this key stops working right away. This can&apos;t be undone.</AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={() => {
                if (!revoking) return;
                revoke.mutate(
                  { params: { path: { id: revoking.id } } },
                  {
                    onSuccess: () => {
                      refresh();
                      toast("Key revoked");
                    },
                    onError: (err) => toast.error(errorMessage(err)),
                  },
                );
              }}
            >
              Revoke key
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}
