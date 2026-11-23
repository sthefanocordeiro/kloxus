import { useQuery } from "@tanstack/react-query";
import { api } from "../api";

// usePublishing reports KLOXUS_PUBLISHING (KLOX-122). Until the
// workspace loads it is false: the reader-facing surface (send, readers,
// public pages, paywall, pricing) must never flash while the app boots.
export function usePublishing(): boolean {
  const ws = useQuery({ queryKey: ["workspace"], queryFn: api.workspace, retry: false, staleTime: Infinity });
  return ws.data?.publishing === true;
}
