import { useQuery, useMutation } from "@tanstack/react-query";
import { useHttp } from "@/hooks/use-ws";
import { queryKeys } from "@/lib/query-keys";

export interface StorageConfig {
  currentBackend: string;
  basePath: string;
}

export interface StorageBackend {
  name: string;
  label: string;
}

export interface StorageBackendsList {
  backends: StorageBackend[];
}

export interface MigrationResponse {
  migrationId: string;
  status: string;
}

export interface MigrationStatus {
  status: "pending" | "running" | "completed" | "failed";
  progress: {
    total: number;
    migrated: number;
  };
  errorMessage?: string;
}

export function useStorageConfig() {
  const http = useHttp();

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: queryKeys.storage.config,
    queryFn: () => http.get<StorageConfig>("/v1/storage/config"),
    staleTime: 60_000,
  });

  return {
    config: data ?? null,
    loading: isLoading,
    error: error instanceof Error ? error.message : null,
    refetch,
  };
}

export function useStorageBackends() {
  const http = useHttp();

  const { data, isLoading, error } = useQuery({
    queryKey: queryKeys.storage.backends,
    queryFn: () => http.get<StorageBackendsList>("/v1/storage/backends"),
    staleTime: 5 * 60_000, // 5 minutes
  });

  return {
    backends: data?.backends ?? [],
    loading: isLoading,
    error: error instanceof Error ? error.message : null,
  };
}

export function useMigrateStorage(onSuccess?: () => void) {
  const http = useHttp();

  const { mutate, isPending, error, reset } = useMutation({
    mutationFn: async (targetBackend: string) => {
      const response = await http.post<MigrationResponse>("/v1/storage/migrate", {
        targetBackend,
      });
      return response;
    },
    onSuccess,
  });

  return {
    migrate: mutate,
    loading: isPending,
    error: error instanceof Error ? error.message : null,
    reset,
  };
}

export function useMigrationStatus(
  migrationId: string | null,
  onComplete?: (status: MigrationStatus) => void
) {
  const http = useHttp();

  const { data, isLoading, error, refetch } = useQuery({
    queryKey: migrationId ? queryKeys.storage.migration(migrationId) : ["storage", "migration", null],
    queryFn: async () => {
      if (!migrationId) return null;
      return http.get<MigrationStatus>(`/v1/storage/migrate/status/${migrationId}`);
    },
    enabled: !!migrationId,
    refetchInterval: (query) => {
      const status = query.state.data;
      if (!status || status.status === "completed" || status.status === "failed") {
        onComplete?.(status!);
        return false;
      }
      return 2000; // Poll every 2 seconds
    },
    staleTime: 0,
  });

  return {
    status: data ?? null,
    loading: isLoading,
    error: error instanceof Error ? error.message : null,
    refetch,
  };
}
