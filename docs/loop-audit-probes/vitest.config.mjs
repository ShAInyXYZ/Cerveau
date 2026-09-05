import panelConfig from '../../panel/vite.config.js';
export default {
  ...panelConfig,
  test: { include: ['../docs/loop-audit-probes/*.test.ts'], environment: 'node', globals: true }
};
