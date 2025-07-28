import { Parser, Token, Tokens, TokensList } from "marked";
import { MarkdownModuleConfig, MARKED_OPTIONS, MarkedRenderer } from "ngx-markdown";

const matchCustomEmbedRegEx = /^\[(video|audio|image)-embedded#]\((.*?)\)/;

//https://regexr.com/3dj5t
const matchYoutubeRegEx = /^((?:https?:)?\/\/)?((?:www|m)\.)?((?:youtube\.com|youtu.be))(\/(?:[\w\-]+\?v=|embed\/|v\/)?)(?<id>[\w\-]+)(\S+)?$/;

const customEmbedExtension = {
  extensions: [{
    name: 'custom_embed',
    level: 'inline',
    start: (src: string) => src.match(matchCustomEmbedRegEx)?.index ?? src.match(matchYoutubeRegEx)?.index,
    tokenizer: (src: string, tokens: Token[] | TokensList) => {

      let match = src.match(matchCustomEmbedRegEx);
      if (match) {
        return {
          type: 'custom_embed',
          raw: match[0],
          meta: { type: match[1], url: match[2] },
        };
      }

      match = src.match(matchYoutubeRegEx);
      if (match && match.groups?.['id']) {
        return {
          type: 'custom_embed',
          raw: match[0],
          meta: { type: 'youtube', id: match.groups['id'] },
        };
      }

      return undefined;
    },
    renderer: (token: Tokens.Generic) => {
      const { type, url, id } = token['meta'];
      switch (type) {
        case 'video':
          return `<div style="max-width: 300px; height: auto;"><video controls style="width: 100%; height: auto;"><source src="${url}" type="video/mp4"></video></div>`;
        case 'audio':
          return `<div><audio src="${url}" controls></audio></div>`;
        case 'image':
          return `<div style="max-width: 300px; height: auto;"><img src="${url}" class="img-fluid" width="300px"></div>`;
        case 'youtube':
          return `<div style="position: relative;"><img youtubeid="${id}" src="https://ytimg.googleusercontent.com/vi/${id}/hqdefault.jpg" class="img-fluid" width="300px"><i
          class="bi bi-youtube" youtubeid="${id}" style="position: absolute; place-self: anchor-center; color: red; font-size: 70px;"></i></div>`;
        default:
          return '';
      }
    }
  }]
}

const renderer = new MarkedRenderer();
// renderer.paragraph = ({ tokens }) => Parser.parseInline(tokens);

export const MarkdownConfig: MarkdownModuleConfig = {
  markedExtensions: [customEmbedExtension],
  markedOptions: {
    provide: MARKED_OPTIONS,
    useValue: {
      renderer: renderer,
      breaks: true,
    },
  }
}