package command

import (
	"bufio"
	"bytes"
	"io"
	"log"
	"os"
	"strconv"
	"sync"
)

const readbuf = 1 << 10 //1024

type flags struct {
	line   int
	lines  int
	size   string
	offset string
	output string
	file   string
}

var args flags

func init() {
	Tail.Flag.StringVar(&args.output, "o", "", "-o 指定输出的路径,不指定则输出到标准输出")
	Tail.Flag.IntVar(&args.line, "l", 0, "-l 指定从倒数第几行开始读取")
	Tail.Flag.IntVar(&args.lines, "n", 0, "-n 指定读取的行数")
	Tail.Flag.StringVar(&args.offset, "i", "", "-i 指定开始读取的位置,单位:b,kb,mb,默认单位:b")
	Tail.Flag.StringVar(&args.size, "s", "", "-s 指定读取的大小,单位:b,kb,mb,默认单位:b")
	Tail.Flag.StringVar(&args.file, "f", "", "-f 指定要查看的文件路径")
}

var Tail = &Command{
	UsageLine: `tail -f main.go -l 10 -n 5 -o tmp.txt`,
	Run:       tail,
	Short:     "从文件结尾或指定位置读取内容",
	Long: `从文件结尾或指定位置读取内容,可以按行读取,也可以按大小读取,-i 和 -l同时使用的话-i生效,-s 与 -n 
同时使用的话-s生效
`,
}

func tail(cmd *Command, arg []string) bool {
	File, err := os.OpenFile(args.file, os.O_RDONLY, 0644)
	if err != nil {
		log.Printf("打开文件失败:%s\n", err.Error())
		return true
	}
	var w io.WriteCloser
	if args.output != "" {
		w, err = os.Create(args.output)
		if err != nil {
			log.Printf("创建输出文件失败:%s\n", err.Error())
			return true
		}
		defer w.Close()
	} else {
		w = os.Stdout
	}
	f := NewTail(File)
	defer f.Close()

	if args.offset != "" {
		offset := parseCompany(args.offset)
		size := parseCompany(args.size)
		if size == 0 && int64(args.lines) == 0 {
			return false
		}
		if err = f.Read(offset, size, int64(args.lines), w); err != nil {
			log.Printf("读取内容错误:%s\n", err.Error())
		}
		return true
	}
	if args.line > 0 {
		if err = f.TailLine(args.line, args.lines, w); err != nil {
			log.Printf("读取内容错误:%s\n", err.Error())
		}
		return true
	}
	return false
}

func parseCompany(c string) int64 {
	if len(c) < 1 {
		return 0
	}

	if c[len(c)-1] != 'b' {
		c += "b"
	}

	index := len(c) - 2
	var company int64 = 1
	switch c[index] {
	case 'k':
		company = 1 << 10 //1024
	case 'm':
		company = 1 << 20 //1024*1024
	default:
		index++
	}
	size, err := strconv.ParseInt(c[:index], 10, 0)
	if err != nil {
		return -1
	}
	return size * company
}

func NewTail(File *os.File) *TailFile {
	offset, _ := File.Seek(0, 2)
	return &TailFile{file: File, size: offset, offset: offset}
}

type TailFile struct {
	mu     sync.Mutex
	file   *os.File
	size   int64
	offset int64
}

func (f *TailFile) read(p []byte) (n int, err error) {
	if f.offset == 0 {
		return 0, io.EOF
	}
	var (
		offset int
		length = int64(len(p))
	)
	if f.offset >= length {
		f.offset -= length
	} else {
		offset = int(f.offset)
		f.offset = 0
	}
	_, err = f.file.Seek(f.offset, 0)
	if err == nil {
		n, err = f.file.Read(p)
		if offset != 0 && offset < n {
			n = offset
		}
	}
	return
}

func (f *TailFile) Read(offset, size, lines int64, w io.Writer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.size < offset {
		return io.EOF
	}
	f.file.Seek(offset, 0)
	switch {
	case size > 0:
		if _, err := io.CopyN(w, f.file, size); err != io.EOF {
			return err
		}
	case lines > 0:
		buf := bufio.NewReader(f.file)
		for i := 0; i < int(lines); i++ {
			line, err := buf.ReadBytes('\n')
			if err != nil {
				return err
			}
			if _, err = w.Write(line); err != nil {
				return err
			}
		}
	}
	return nil
}

//暂时用不到
func (f *TailFile) ReadLine() ([]byte, error) {
	var list []*[]byte
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.offset == 0 {
		return nil, io.EOF
	}

	for {
		var buf = make([]byte, 256)
		n, err := f.read(buf[:])
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		index := bytes.LastIndexByte(buf[:n], '\n')
		if index >= 0 {
			f.offset += int64(index)
			f.file.Seek(f.offset, 0)
			buf = buf[index:n]
			list = append(list, &buf)
			break
		} else {
			buf = buf[:n]
			list = append(list, &buf)
		}
	}

	var (
		last int
		line = make([]byte, len(list)*256)
	)

	for i := len(list) - 1; i >= 0; i-- {
		copy(line[last:], *list[i])
		last += len(*list[i])
	}

	return line[:last], nil
}

func (f *TailFile) TailLine(line, lines int, w io.Writer) (err error) {
	buf := make([]byte, readbuf)

	f.mu.Lock()
	defer f.mu.Unlock()
	var (
		n, l int
		sep  = []byte("\n")
	)
	for {
		n, err = f.read(buf)
		if err != nil {
			if err == io.EOF {
				f.file.Seek(0, 0)
				break
			}
		}
		l += bytes.Count(buf[:n], sep)
		if l >= line {
			var seek, i int
			buf = buf[:n]
			for l > line {
				i = bytes.Index(buf, sep) + 1
				buf = buf[i:]
				seek += i
				l--
			}
			f.file.Seek(f.offset+int64(seek), 0)
			break
		}
	}
	if lines == 0 {
		_, err = io.Copy(w, f.file)
	} else {
		buf := bufio.NewReader(f.file)
		var line []byte
		for i := 0; i < lines; i++ {
			line, err = buf.ReadBytes('\n')
			if err != nil {
				return
			}
			if _, err = w.Write(line); err != nil {
				return
			}
		}
	}
	return
}

func (f *TailFile) Close() error {
	return f.file.Close()
}
