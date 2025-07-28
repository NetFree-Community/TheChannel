import { Injectable } from '@angular/core';

@Injectable({
    providedIn: 'root'
})
export class SoundService {
    private audio: HTMLAudioElement | null = null;
    private enabled: boolean = true;
    private initialized: boolean = false;
    private readonly SOUND_PREFERENCE_KEY = 'soundEnabled';

    constructor() {
        this.loadSoundPreference();
        this.preloadSound();
    }

    private loadSoundPreference(): void {
        try {
            const savedPreference = localStorage.getItem(this.SOUND_PREFERENCE_KEY);
            if (savedPreference !== null) {
                this.enabled = JSON.parse(savedPreference);
            }
        } catch (error) {
            console.warn('Failed to load sound preference:', error);
        }
    }

    private saveSoundPreference(): void {
        try {
            localStorage.setItem(this.SOUND_PREFERENCE_KEY, JSON.stringify(this.enabled));
        } catch (error) {
            console.warn('Failed to save sound preference:', error);
        }
    }

    private saveSoundPreferenceToCookie(): void {
        try {
            const expirationDays = 365;
            const expirationDate = new Date();
            expirationDate.setTime(expirationDate.getTime() + (expirationDays * 24 * 60 * 60 * 1000));

            document.cookie = `${this.SOUND_PREFERENCE_KEY}=${this.enabled}; expires=${expirationDate.toUTCString()}; path=/; SameSite=Strict`;
        } catch (error) {
            console.warn('Failed to save sound preference to cookie:', error);
        }
    }

    private loadSoundPreferenceFromCookie(): void {
        try {
            const cookieValue = document.cookie
                .split('; ')
                .find(row => row.startsWith(`${this.SOUND_PREFERENCE_KEY}=`))
                ?.split('=')[1];

            if (cookieValue !== undefined) {
                this.enabled = cookieValue === 'true';
            }
        } catch (error) {
            console.warn('Failed to load sound preference from cookie:', error);
        }
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
        this.saveSoundPreference();
    }

    disableSound() {
        this.enabled = false;
        this.saveSoundPreference();
    }

    toggleSound(): boolean {
        this.enabled = !this.enabled;
        this.saveSoundPreference();
        return this.enabled;
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

    clearSavedPreferences(): void {
        try {
            localStorage.removeItem(this.SOUND_PREFERENCE_KEY);
            document.cookie = `${this.SOUND_PREFERENCE_KEY}=; expires=Thu, 01 Jan 1970 00:00:00 UTC; path=/;`;
        } catch (error) {
            console.warn('Failed to clear saved preferences:', error);
        }
    }
}