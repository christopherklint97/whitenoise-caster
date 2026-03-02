import { describe, it, expect } from 'vitest';
import { formatCountdown, getWheelValue } from '../timer';

describe('formatCountdown', () => {
  it('returns 00:00 for zero', () => {
    expect(formatCountdown(0)).toBe('00:00');
  });

  it('returns 00:00 for negative values', () => {
    expect(formatCountdown(-5)).toBe('00:00');
  });

  it('formats seconds under a minute', () => {
    expect(formatCountdown(59)).toBe('00:59');
  });

  it('formats exactly one minute', () => {
    expect(formatCountdown(60)).toBe('01:00');
  });

  it('formats minutes and seconds', () => {
    expect(formatCountdown(754)).toBe('12:34');
  });

  it('formats exactly one hour', () => {
    expect(formatCountdown(3600)).toBe('1:00:00');
  });

  it('formats hours, minutes, and seconds', () => {
    expect(formatCountdown(3661)).toBe('1:01:01');
  });

  it('formats large values', () => {
    expect(formatCountdown(43200)).toBe('12:00:00');
  });

  it('pads single digits', () => {
    expect(formatCountdown(1)).toBe('00:01');
    expect(formatCountdown(61)).toBe('01:01');
  });
});

describe('getWheelValue', () => {
  it('returns 0 for scrollTop 0', () => {
    const container = { scrollTop: 0 } as HTMLElement;
    expect(getWheelValue(container)).toBe(0);
  });

  it('rounds to nearest item', () => {
    const container = { scrollTop: 66 } as HTMLElement;
    expect(getWheelValue(container)).toBe(2);
  });

  it('never returns negative', () => {
    const container = { scrollTop: -10 } as HTMLElement;
    expect(getWheelValue(container)).toBe(0);
  });
});
