import { Injectable } from '@angular/core';

@Injectable({
    providedIn: 'root'
})
export class SoundService {
    private audio: HTMLAudioElement | null = null;
    private enabled: boolean = true;
    private initialized: boolean = false;

    constructor() {
        this.preloadSound();
    }

    private preloadSound() {
        try {
            this.audio = new Audio('assets/notification.mp3');
            this.audio.preload = 'auto';
            this.audio.volume = 0.5;
        } catch (error) {
            console.warn('Failed to load sound file:', error);
        }
    }

    async initializeAudioContext(): Promise<void> {
        if (this.initialized || !this.audio) return;

        try {
            this.audio.muted = true;
            const playPromise = this.audio.play();

            if (playPromise) {
                await playPromise;
                this.audio.pause();
                this.audio.currentTime = 0;
                this.audio.muted = false;
                this.initialized = true;
            }
        } catch (error) {
            console.warn('Failed to initialize audio context:', error);
        }
    }

    async playNotificationSound(): Promise<void> {
        if (!this.enabled || !this.audio) return;

        if (!this.initialized) {
            await this.initializeAudioContext();
        }

        try {
            this.audio.currentTime = 0;
            await this.audio.play();
        } catch (error) {
            console.warn('Cannot play notification sound:', error);
            this.requestUserInteraction();
        }
    }

    private requestUserInteraction() {
        console.info('לאפשור צלילי התראה, לחצו על כפתור הצלילים בכותרת');
    }

    enableSound() {
        this.enabled = true;
    }

    disableSound() {
        this.enabled = false;
    }

    isEnabled(): boolean {
        return this.enabled;
    }

    isInitialized(): boolean {
        return this.initialized;
    }

    setVolume(volume: number) {
        if (this.audio && volume >= 0 && volume <= 1) {
            this.audio.volume = volume;
        }
    }
}