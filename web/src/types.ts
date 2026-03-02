export type State = 'disconnected' | 'connecting' | 'playing' | 'paused' | 'error';

export interface TimerInfo {
  active: boolean;
  remaining_s: number;
  action: 'stop' | 'volume';
  volume_level: number;
}

export interface StatusResponse {
  state: State;
  speaker_ip?: string;
  speaker_name?: string;
  error?: string;
  timer: TimerInfo;
}

export interface Speaker {
  ip: string;
  name: string;
}

export interface PlayRequest {
  speaker_ip: string;
}

export interface VolumeRequest {
  level: number;
}

export interface TimerRequest {
  duration_s: number;
  action: 'stop' | 'volume';
  volume_level?: number;
}
