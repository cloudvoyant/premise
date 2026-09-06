import { describe, it, expect } from 'vitest';
import { greet } from '../src/lib';

describe('greet', () => {
  it('returns a greeting with the given name', () => {
    expect(greet({ name: 'world' })).toBe('Hello, world!');
  });

  it('handles different names', () => {
    expect(greet({ name: 'mise' })).toBe('Hello, mise!');
  });
});
