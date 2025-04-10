import { Component } from '@angular/core';
import { NbCardModule } from '@nebular/theme';

@Component({
  selector: 'app-markdown-help',
  standalone: true,
  imports: [
    NbCardModule
  ],
  templateUrl: './markdown-help.component.html',
  styleUrl: './markdown-help.component.scss'
})
export class MarkdownHelpComponent {
  // Official Markdown documentation URL
  markdownDocsUrl: string = 'https://www.markdownguide.org/basic-syntax/';
}
