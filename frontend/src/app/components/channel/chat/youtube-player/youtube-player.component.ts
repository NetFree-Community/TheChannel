import { Component, OnInit } from '@angular/core';
import { NbButtonModule, NbDialogRef, NbIconModule, NbTooltipModule } from '@nebular/theme';
import { YouTubePlayer } from "@angular/youtube-player";


@Component({
  selector: 'app-youtube-player',
  imports: [
    YouTubePlayer,
    NbButtonModule,
    NbIconModule,
    NbTooltipModule,
  ],
  templateUrl: './youtube-player.component.html',
  styleUrl: './youtube-player.component.scss'
})
export class YoutubePlayerComponent implements OnInit {
  // 16:9, as wide as the viewport allows with a margin on either side.
  iframeWidth = Math.min(window.innerWidth * 0.92, 960);
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
