import { describe, it, expect } from 'vitest';
import { PARAMS, profileLabel, modelName } from './vocabulary';

describe('vocabulary', () => {
  it('drops the engine from a profile name, and only the engine', () => {
    expect(profileLabel('vLLM · BF16 · TP=4', 'vLLM')).toBe('BF16 · TP=4');
    expect(profileLabel('llama.cpp · MoE', 'llama.cpp')).toBe('MoE');   // the dot is not a wildcard
    expect(profileLabel('vLLM', 'vLLM')).toBe('vLLM');                   // never an empty label
    expect(profileLabel(undefined, 'vLLM')).toBe('');
  });

  it('says a parameter the way a person would', () => {
    expect(PARAMS.GPU_UTIL.say!('0.92')).toBe('92%');
    expect(PARAMS.MAX_LEN.say!('262144')).toBe('256K');
    expect(PARAMS.CUDA_VISIBLE_DEVICES.say!('0,1, 2,3')).toBe('0 · 1 · 2 · 3');
    expect(PARAMS.DRAFT_TOKENS.say!('0')).toBe('off');
    expect(PARAMS.VISION.say!('1')).toBe('on');
  });

  it('names a checkpoint by the end of its path', () => {
    expect(modelName('%h/models/Qwen3.8-27B-W8A16-MTP/')).toBe('Qwen3.8-27B-W8A16-MTP');
    expect(modelName(undefined)).toBe('');
  });
});
