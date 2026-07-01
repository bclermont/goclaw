import { describe, it, expect } from 'vitest';
import { useStorageConfig, useStorageBackends, useMigrateStorage, useMigrationStatus } from './useStorageAPI';

describe('useStorageAPI', () => {
  it('exports all hooks', () => {
    expect(useStorageConfig).toBeDefined();
    expect(useStorageBackends).toBeDefined();
    expect(useMigrateStorage).toBeDefined();
    expect(useMigrationStatus).toBeDefined();
  });
});
