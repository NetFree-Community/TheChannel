import { Component, OnInit } from '@angular/core';
import { NbDialogRef } from '@nebular/theme';
import { YouTubePlayer } from "@angular/youtube-player";
import { CommonModule } from '@angular/common';

@Component({
  selector: 'app-youtube-player',
  imports: [
    YouTubePlayer,
    CommonModule
  ],
  templateUrl: './youtube-player.component.html',
  styleUrl: './youtube-player.component.scss'
})
export class YoutubePlayerComponent implements OnInit {
  iframeWidth = window.innerWidth / 100 * 80;
  iframeHeight = this.iframeWidth * 9 / 16;

  constructor(private dialogRef: NbDialogRef<YoutubePlayerComponent>) { }

  videoId: string = '';
  playerConfig = {
    autoplay: 1
  }

  ngOnInit(): void {
    this.videoId = this.dialogRef.componentRef.instance.videoId;
  }

  closeDialog() {
    this.dialogRef.close();
  };
}
