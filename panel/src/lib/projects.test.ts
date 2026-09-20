import { describe, it, expect } from 'vitest';
import { groupByProject, filterProjects } from './projects.js';

const s = (id: string, name: string, workspace: string, created = '2026-09-01T00:00:00Z') => ({ id, name, workspace, created });
const SESSIONS = [
  s('s_racer9', 'racer9', '/home/u/GitHub/racer9'),
  s('s_mk1', 'VoxelWorld', '/home/u/GitHub/VoxelWorld', '2026-09-07T00:00:00Z'),
  s('s_mk2', 'lighting pass', '/home/u/GitHub/VoxelWorld'),
  s('s_fan', 'fan curve', '/home/u/GitHub/Fan'),
];

describe('filterProjects', () => {
  const projects = groupByProject(SESSIONS);

  it('returns everything for an empty query', () => {
    expect(filterProjects(projects, '   ')).toBe(projects);
  });

  it('keeps every session of a project matched by name or path', () => {
    const got = filterProjects(projects, 'voxelworld');
    expect(got.map((p: { name: string }) => p.name)).toEqual(['VoxelWorld']);
    expect(got[0].sessions).toHaveLength(2);
    expect(filterProjects(projects, 'github/fan').map((p: { name: string }) => p.name)).toEqual(['Fan']);
  });

  it('keeps only the matching sessions when the project itself does not match', () => {
    const got = filterProjects(projects, 'lighting');
    expect(got).toHaveLength(1);
    expect(got[0].sessions.map((x: { id: string }) => x.id)).toEqual(['s_mk2']);
    // by id too — that is what the rail shows under an idle session
    expect(filterProjects(projects, 's_fan')[0].name).toBe('Fan');
  });

  it('needs every word, in any order and any case', () => {
    expect(filterProjects(projects, 'RACER 9').map((p: { name: string }) => p.name)).toEqual(['racer9']);
    expect(filterProjects(projects, 'racer zebra')).toEqual([]);
  });
});
