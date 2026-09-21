import {
  waitForEvenAppBridge, CreateStartUpPageContainer, RebuildPageContainer,
  TextContainerProperty, ImageContainerProperty, TextContainerUpgrade, ImageRawDataUpdate,
  ListContainerProperty, ListItemContainerProperty,
} from '@evenrealities/even_hub_sdk'

type PageStyle = { X: number; Y: number; Width: number; Height: number; BorderWidth: number; BorderColor: number; BorderRadius: number; PaddingLength: number; ListItemWidth: number }

type Page = {
  list?: { items: string[]; itemWidth: number; selectBorder: boolean }
  info?: { text: string; style: PageStyle }
  text: string
  style: PageStyle
  icon: { ID: number; Name: string; X: number; Y: number; Width: number; Height: number; BMP: string } | null
  divider?: { ID: number; Name: string; X: number; Y: number; Width: number; Height: number; BMP: string }
}

async function main() {
  const bridge = await waitForEvenAppBridge()
  let created = false
  let previousLayout = '', previousText = '', previousBMP = '', previousDividerBMP = ''
  async function render() {
    try {
      const response = await fetch('/frame', { cache: 'no-store' })
      if (!response.ok) throw new Error(`读取页面失败：${response.status}`)
      const page: Page = await response.json()
      const { style, icon, divider } = page
      const text = new TextContainerProperty({
        containerID: 1, containerName: 'agent-stream', isEventCapture: 1,
        xPosition: style.X, yPosition: style.Y, width: style.Width, height: style.Height,
        borderWidth: style.BorderWidth, borderColor: style.BorderColor,
        borderRadius: style.BorderRadius, paddingLength: style.PaddingLength,
        content: page.text || ' ',
      })
      const image = icon ? new ImageContainerProperty({
        containerID: icon.ID, containerName: icon.Name,
        xPosition: icon.X, yPosition: icon.Y, width: icon.Width, height: icon.Height,
      }) : null
      const dividerImage = divider ? new ImageContainerProperty({
        containerID: divider.ID, containerName: divider.Name,
        xPosition: divider.X, yPosition: divider.Y, width: divider.Width, height: divider.Height,
      }) : null
      const info = page.info ? new TextContainerProperty({
        containerID: 3, containerName: 'list-info', isEventCapture: 0,
        xPosition: page.info.style.X, yPosition: page.info.style.Y,
        width: page.info.style.Width, height: page.info.style.Height,
        borderWidth: page.info.style.BorderWidth, borderColor: page.info.style.BorderColor,
        borderRadius: page.info.style.BorderRadius, paddingLength: page.info.style.PaddingLength,
        content: page.info.text,
      }) : null
      const layout = JSON.stringify([style, image, dividerImage, page.list, page.info])
      const containers = page.list ? {
        containerTotalNum: 1 + (info ? 1 : 0) + (image ? 1 : 0) + (dividerImage ? 1 : 0),
        listObject: [new ListContainerProperty({
          containerID: 1, containerName: 'agent-stream', isEventCapture: 1,
          xPosition: style.X, yPosition: style.Y, width: style.Width, height: style.Height,
          borderWidth: style.BorderWidth, borderColor: style.BorderColor,
          borderRadius: style.BorderRadius, paddingLength: style.PaddingLength,
          itemContainer: new ListItemContainerProperty({
            itemCount: page.list.items.length, itemWidth: page.list.itemWidth,
            isItemSelectBorderEn: page.list.selectBorder ? 1 : 0, itemName: page.list.items,
          }),
        })], textObject: info ? [info] : [], imageObject: [dividerImage, image].filter((value): value is ImageContainerProperty => value !== null),
      } : { containerTotalNum: image ? 2 : 1, textObject: [text], imageObject: image ? [image] : [] }
      if (!created) {
        const result = await bridge.createStartUpPageContainer(new CreateStartUpPageContainer(containers))
        if (result !== 0) throw new Error(`创建页面失败：${result}`)
        created = true
        previousLayout = layout
        previousText = page.text
      } else if (layout !== previousLayout) {
        if (!await bridge.rebuildPageContainer(new RebuildPageContainer(containers))) throw new Error('重建页面失败')
        previousLayout = layout
        previousText = page.text
        previousBMP = ''
        previousDividerBMP = ''
      } else if (page.text !== previousText) {
        if (!await bridge.textContainerUpgrade(new TextContainerUpgrade({
          containerID: 1, containerName: 'agent-stream', content: page.text || ' ', contentOffset: 0, contentLength: 0,
        }))) throw new Error('更新文本失败')
        previousText = page.text
      }
      if (divider && divider.BMP !== previousDividerBMP) {
        const result = await bridge.updateImageRawData(new ImageRawDataUpdate({
          containerID: divider.ID, containerName: divider.Name,
          imageData: Uint8Array.from(atob(divider.BMP), char => char.charCodeAt(0)),
        }))
        if (result !== 'success') throw new Error(`更新分割线失败：${result}`)
        previousDividerBMP = divider.BMP
      }
      if (icon && icon.BMP !== previousBMP) {
        const result = await bridge.updateImageRawData(new ImageRawDataUpdate({
          containerID: icon.ID, containerName: icon.Name,
          imageData: Uint8Array.from(atob(icon.BMP), char => char.charCodeAt(0)),
        }))
        if (result !== 'success') throw new Error(`更新图标失败：${result}`)
        previousBMP = icon.BMP
      }
      document.getElementById('status')!.textContent = '页面已同步'
    } catch (error) {
      console.error(error)
      document.getElementById('status')!.textContent = String(error)
    }
    window.setTimeout(render, 250)
  }
  await render()
}

void main().catch(error => {
  console.error(error)
  document.getElementById('status')!.textContent = String(error)
})
