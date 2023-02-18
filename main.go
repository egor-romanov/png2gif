package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/gif"
	_ "image/jpeg"
	_ "image/png"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/sync/errgroup"

	"github.com/vitali-fedulov/images4"
)

func main() {
	p := tea.NewProgram(initialModel())

	if _, err := p.Run(); err != nil {
		log.Fatal(err)
	}
}

// errMsg is a type for error message
type (
	errMsg error
)

// `resultMsg` is a struct that contains a `duration`, `emoji`, and an `err`.
// @property duration - The time it took to run the command.
// @property {string} emoji - The emoji that will be displayed in the message.
// @property {error} err - This is the error that occurred during the execution of the function.
type resultMsg struct {
	duration time.Duration
	emoji    string
	err      error
}

// ImgWithDelay is a struct that contains an image.Image and an delay in numbers of frames.
// @property img - The image.Image object that represents the frame.
// @property {int} delay - The delay in numbers of frames before the next image is shown.
type imgWithDelay struct {
	img   image.Image
	delay int
}

// PalettedWithDelay is a struct that contains an image.Paletted and an delay in numbers of frames.
// @property paletted - The image.Paletted object that represents the frame.
// @property {int} delay - The delay in numbers of frames before the next image is shown.
type palettedWithDelay struct {
	paletted *image.Paletted
	delay    int
}

// input fields in the form
const (
	path = iota
	output
	fps
)

// thy and thCbCr are the threshold for the YCbCr color model to check if images are equal.
const (
	thy    = float64(100)
	thCbCr = float64(200)
)

// hotPink and darkGray are the colors used in the UI.
const (
	hotPink  = lipgloss.Color("#FF06B7")
	darkGray = lipgloss.Color("#767676")
)

// inputStyle and continueStyle are the styles for inputs.
var (
	inputStyle    = lipgloss.NewStyle().Foreground(hotPink)
	continueStyle = lipgloss.NewStyle().Foreground(darkGray)
)

// model is the main model for the tea app.
// @property {[]textinput.Model} inputs - the models for the text inputs.
// @property {int} focused - The index of the input that is currently focused.
// @property {spinner.Model} spinner - The spinner model.
// @property {bool} loading - Whether the app is currently processing images.
// @property {time.Duration} duration - The duration of the processing.
// @property {bool} finished - Whether the current processing pipe has finished.
// @property {error} err - This is the error that will be displayed if any errors happen.
type model struct {
	inputs   []textinput.Model
	focused  int
	spinner  spinner.Model
	loading  bool
	duration time.Duration
	finished bool
	err      error
}

// Validator functions to ensure valid input
func fpsValidator(s string) error {
	// fps should be a number
	c := strings.ReplaceAll(s, " ", "")
	_, err := strconv.ParseInt(c, 10, 64)

	return err
}

// initialize app model.
func initialModel() model {
	var inputs []textinput.Model = make([]textinput.Model, 3)
	inputs[path] = textinput.New()
	inputs[path].Placeholder = "/path/to/folder/"
	inputs[path].Focus()
	inputs[path].Width = 30
	inputs[path].Prompt = ""

	inputs[output] = textinput.New()
	inputs[output].Placeholder = "output.gif"
	inputs[output].Width = 20
	inputs[output].Prompt = ""

	inputs[fps] = textinput.New()
	inputs[fps].Placeholder = "30"
	inputs[fps].CharLimit = 2
	inputs[fps].Width = 5
	inputs[fps].Prompt = ""
	inputs[fps].Validate = fpsValidator

	sp := spinner.New()
	sp.Spinner = spinner.MiniDot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("206"))

	return model{
		inputs:  inputs,
		focused: 0,
		spinner: sp,
		err:     nil,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick)
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd = make([]tea.Cmd, len(m.inputs))

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			// if the app is currently processing images, then we don't want to do anything.
			if m.loading {
				return m, nil
			}

			// if the app has finished processing images, then we want to reset the model.
			if m.finished || m.err != nil {
				w := m.inputs[path].Width
				sp := m.spinner
				m = initialModel()
				m.inputs[path].Width = w
				m.inputs[output].Width = w / 2
				m.inputs[fps].Width = w / 2
				m.spinner = sp
				return m, nil
			}

			// if the current input is the last input, then we want to start processing images.
			if m.focused == len(m.inputs)-1 {
				m.loading = true
				for i := range m.inputs {
					m.inputs[i].Blur()
				}
				return m, gen(m.inputs[path].Value(), m.inputs[output].Value(), m.inputs[fps].Value())
			}

			// otherwise, we want to move to the next input.
			m.nextInput()

		// quit app
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit

		// navigate between inputs
		case tea.KeyShiftTab, tea.KeyCtrlP:
			if m.loading || m.finished || m.err != nil {
				return m, nil
			}

			m.prevInput()
		case tea.KeyTab, tea.KeyCtrlN:
			if m.loading || m.finished || m.err != nil {
				return m, nil
			}

			m.nextInput()
		}

		for i := range m.inputs {
			m.inputs[i].Blur()
		}
		m.inputs[m.focused].Focus()

	// Check terminal size
	case tea.WindowSizeMsg:
		m.inputs[path].Width = msg.Width
		m.inputs[output].Width = msg.Width / 2
		m.inputs[fps].Width = msg.Width / 2

	// Handle results
	case resultMsg:
		m.loading = false
		m.inputs[path].Focus()
		if msg.err != nil {
			m.err = msg.err
			return m, nil
		}
		m.finished = true
		return m, nil

	// We handle errors just like any other message
	case errMsg:
		m.err = msg
		return m, nil
	}

	// Update inputs
	for i := range m.inputs {
		m.inputs[i], cmds[i] = m.inputs[i].Update(msg)
	}
	var cmdSpin tea.Cmd
	m.spinner, cmdSpin = m.spinner.Update(msg)
	cmds = append(cmds, cmdSpin)
	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	pad := strings.Repeat(" ", 2)

	// Render processing spinner
	if m.loading {
		return "\n\n" + pad + pad + m.spinner.View() + "  processing...\n"
	}

	// Render error message
	if m.err != nil {
		return "" +
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#bf616a")).
				Copy().
				Width(m.inputs[path].Width).
				PaddingLeft(4).
				PaddingTop(2).
				Render("error: "+m.err.Error()) +
			continueStyle.
				Copy().
				PaddingTop(3).
				PaddingLeft(2).
				Render("Start again ->") +
			"\n"
	}

	// Render success message
	if m.finished {
		filename := "./out.gif"
		if m.inputs[output].Value() != "" {
			filename = m.inputs[output].Value()
		}
		outPath, _ := filepath.Abs(filename)
		return "" +
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#a3be8c")).
				Copy().
				Width(m.inputs[path].Width).
				PaddingTop(1).
				PaddingLeft(2).
				Render("success, open your file: ") +
			lipgloss.NewStyle().
				Foreground(lipgloss.Color("#8fbcbb")).
				Copy().
				PaddingTop(2).
				PaddingLeft(4).
				Width(m.inputs[path].Width).
				Render(outPath) +
			continueStyle.
				Copy().
				PaddingTop(5).
				PaddingLeft(2).
				Render("Continue ->") +
			"\n"
	}

	// Render input fields
	return fmt.Sprintf(
		` 
Generate Gif from a bunch of png or jpg files:

  %s
    %s

  %s
    %s

  %s
    %s

  %s
`,
		inputStyle.Width(m.inputs[path].Width).Render("Path to folder with images:"),
		m.inputs[path].View(),
		inputStyle.Width(m.inputs[output].Width).Render("Output file:"),
		m.inputs[output].View(),
		inputStyle.Width(m.inputs[fps].Width).Render("Frame rate (👉25-50👈):"),
		m.inputs[fps].View(),
		continueStyle.Render("Continue ->"),
	) + "\n"
}

// nextInput focuses the next input field
func (m *model) nextInput() {
	m.focused = (m.focused + 1) % len(m.inputs)
}

// prevInput focuses the previous input field
func (m *model) prevInput() {
	m.focused--
	// Wrap around
	if m.focused < 0 {
		m.focused = len(m.inputs) - 1
	}
}

// gen is the func that generates the gif
func gen(path, output, fps string) tea.Cmd {
	if output == "" {
		output = "out.gif"
	}
	return func() tea.Msg {
		start := time.Now()
		// list files in path
		paths, err := listFiles(path)
		if err != nil {
			return resultMsg{err: err, emoji: "📂"}
		}

		// parse fps
		c := strings.ReplaceAll(fps, " ", "")
		if c == "" {
			c = "30"
		}
		fpsVal, _ := strconv.ParseInt(c, 10, 64)

		// build gif
		err = BuildGif(
			paths,
			output,
			int(fpsVal),
		)
		if err != nil {
			return resultMsg{err: err, emoji: "🔨"}
		}
		duration := time.Since(start)
		return resultMsg{err: nil, emoji: "🎉", duration: duration}
	}
}

/* ------------------------------------------------------------ */
/* --------------------- WORK WITH IMAGES --------------------- */
/* ------------------------------------------------------------ */

// list files in path
func listFiles(path string) (*[]string, error) {
	var files []string
	dir, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer dir.Close()

	fileInfos, err := dir.Readdir(-1)
	if err != nil {
		return nil, err
	}

	for _, fi := range fileInfos {
		if !fi.IsDir() {
			// add file to list if it is not a .png or .jpg
			if filepath.Ext(fi.Name()) == ".png" || filepath.Ext(fi.Name()) == ".jpg" {
				files = append(files, filepath.Join(path, fi.Name()))
			}
		}
	}
	sort.Strings(files)

	return &files, nil
}

func readImages(files *[]string) ([]imgWithDelay, error) {
	// create slice of images
	images := []imgWithDelay{}
	// save previous image to compare with current and count delay (equal images in a row)
	prevImg := image.Image(nil)
	delay := 1

	// read images from files
	for _, s := range *files {
		f, err := os.Open(s)
		if err != nil {
			return nil, fmt.Errorf("failed to open file (%s): %w", s, err)
		}
		defer f.Close()

		img, _, err := image.Decode(f)
		if err != nil {
			return nil, fmt.Errorf("failed to decode image (%s): %w", s, err)
		}

		// if prevImg is not nil, compare it with current image, if they are equal, increase delay,
		// else add previous image to slice of images, reset delay, and set current image as previous
		if prevImg != nil {
			if !imagesEqual(prevImg, img) {
				images = append(images, imgWithDelay{prevImg, delay})
				delay = 1
				prevImg = img
			} else {
				delay++
			}
		} else {
			prevImg = img
		}
	}
	// add last image to slice of images
	images = append(images, imgWithDelay{prevImg, delay})
	return images, nil
}

func imagesEqual(a, b image.Image) bool {
	// Icons are compact image representations (image "hashes").
	// Name "hash" is not used intentionally.
	iconA := images4.Icon(a)
	iconB := images4.Icon(b)

	// Compare icons by proportion similarity metric.
	if images4.PropMetric(iconA, iconB) > 0.001 {
		return false
	}
	// Compare icons by Euclidean distance in YCbCr color space.
	m1, m2, m3 := images4.EucMetric(iconA, iconB)
	if m1 > thy {
		return false
	}
	if m2 > thCbCr || m3 > thCbCr {
		return false
	}
	return true
}

// encode and decode is necessary to convert jpeg and png to gif.
func encodeImgPaletted(images *[]imgWithDelay) ([]*palettedWithDelay, error) {
	// Gif options
	opt := gif.Options{}
	imgp := make([]*palettedWithDelay, len(*images))

	// create a go routine for each image. and wait for all to finish.
	errGroup, _ := errgroup.WithContext(context.Background())
	lck := sync.Mutex{}

	for ctr, im := range *images {
		ctr := ctr
		im := im
		// create a go routine for each image. And wait for all to finish. Check if any errors.
		errGroup.Go(func() error {
			b := bytes.Buffer{}
			// Write file to buffer.
			err := gif.Encode(&b, im.img, &opt)
			if err != nil {
				return err
			}
			// Decode file from buffer to img.
			img, err := gif.Decode(&b)
			if err != nil {
				return err
			}
			// Cast img.
			i, ok := img.(*image.Paletted)
			if ok {
				lck.Lock()
				defer lck.Unlock()
				imgp[ctr] = &palettedWithDelay{i, im.delay}
			}
			return nil
		})
	}

	if err := errGroup.Wait(); err != nil {
		return nil, err
	}
	return imgp, nil
}

// write a file from a paletted image slice, delay in 100ths of a second per frame.
func writeGif(im *[]*palettedWithDelay, delay int, path string) error {
	g := &gif.GIF{}

	for _, i := range *im {
		g.Image = append(g.Image, i.paletted)
		// delay is in 100ths of a second per frame, i.delay represents image repetitions in the source.
		g.Delay = append(g.Delay, delay*i.delay)
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	return gif.EncodeAll(f, g)
}

// BuildGif takes an array of file paths pointing to images as input.
// out: path to the output file.
// fps: frames per second, default 30.
func BuildGif(files *[]string, out string, fps int) error {
	if fps == 0 {
		fps = 30
	}

	img, err := readImages(files)
	if err != nil {
		return err
	}

	im_p, err := encodeImgPaletted(&img)
	if err != nil {
		return err
	}

	return writeGif(&im_p, 100/fps, out)
}
